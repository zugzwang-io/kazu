// Package checkout takes orders and gets them paid: it records each order as
// pending, charges it through payments, and leaves anything with an unknown
// outcome to the reconciler.
package checkout

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const (
	StatusPending = "pending"
	StatusPaid    = "paid"
	StatusFailed  = "failed"
)

type Order struct {
	ID             string `json:"id"`
	IdempotencyKey string `json:"idempotency_key"`
	AmountCents    int64  `json:"amount_cents"`
	Status         string `json:"status"`
}

var ErrNotFound = errors.New("not found")

// Store is checkout's view of the orders table.
type Store interface {
	// CreateOrGet inserts a pending order for key, or returns the one that exists.
	CreateOrGet(ctx context.Context, key string, amountCents int64) (Order, error)
	// Resolve moves a pending order to status and returns the order as it now is;
	// an order that is no longer pending is returned unchanged.
	Resolve(ctx context.Context, id, status string) (Order, error)
	Get(ctx context.Context, id string) (Order, error)
	List(ctx context.Context, status string, limit int) ([]Order, error)
	// ClaimStale returns up to limit orders pending for longer than age and marks
	// them as touched now, so the next pass doesn't pick them up again at once.
	ClaimStale(ctx context.Context, age time.Duration, limit int) ([]Order, error)
}

// Payer charges an order. Implementations own the timeout and retry policy.
type Payer interface {
	Charge(ctx context.Context, o Order) Outcome
}

type Server struct {
	Store Store
	Pay   Payer
	Ready func(context.Context) error // GET /healthz; nil means always ready
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /orders", s.createOrder)
	mux.HandleFunc("GET /orders/{id}", s.getOrder)
	mux.HandleFunc("GET /orders", s.listOrders)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if s.Ready != nil {
			if err := s.Ready(r.Context()); err != nil {
				http.Error(w, err.Error(), http.StatusServiceUnavailable)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func (s *Server) createOrder(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if key == "" || len(key) > 200 {
		http.Error(w, "Idempotency-Key header required (at most 200 characters)", http.StatusBadRequest)
		return
	}
	var body struct {
		AmountCents int64 `json:"amount_cents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.AmountCents <= 0 {
		http.Error(w, "body must be {\"amount_cents\": <positive integer>}", http.StatusBadRequest)
		return
	}

	o, err := s.Store.CreateOrGet(r.Context(), key, body.AmountCents)
	if err != nil {
		slog.Error("create order", "key", key, "err", err)
		http.Error(w, "could not record order", http.StatusServiceUnavailable)
		return
	}
	// A repeat of a key whose payment is still unresolved resumes it.
	if o.Status == StatusPending {
		o = s.Settle(r.Context(), o)
	}

	code := http.StatusOK
	if o.Status == StatusPending {
		code = http.StatusAccepted // the reconciler will finish it
	}
	writeJSON(w, code, o)
}

// Settle charges a pending order and records the outcome. Paid and declined
// outcomes resolve the order; an unknown outcome (a timeout, a reset, a 5xx:
// payments may or may not have charged) leaves it pending for the reconciler,
// which retries with the same idempotency key.
func (s *Server) Settle(ctx context.Context, o Order) Order {
	var to string
	switch s.Pay.Charge(ctx, o) {
	case OutcomePaid:
		to = StatusPaid
	case OutcomeDeclined:
		to = StatusFailed
	default:
		return o
	}
	// The order must be resolved even if the caller has gone away: the charge
	// already happened.
	resolved, err := s.Store.Resolve(context.WithoutCancel(ctx), o.ID, to)
	if err != nil {
		slog.Error("resolve order", "order", o.ID, "to", to, "err", err)
		return o
	}
	return resolved
}

func (s *Server) getOrder(w http.ResponseWriter, r *http.Request) {
	o, err := s.Store.Get(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		http.Error(w, "could not read order", http.StatusServiceUnavailable)
	default:
		writeJSON(w, http.StatusOK, o)
	}
}

func (s *Server) listOrders(w http.ResponseWriter, r *http.Request) {
	orders, err := s.Store.List(r.Context(), r.URL.Query().Get("status"), 100)
	if err != nil {
		http.Error(w, "could not list orders", http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, orders)
}

// Reconcile re-sends orders that have been pending for longer than age, every
// interval, until ctx ends. Up to workers orders are settled at once.
func (s *Server) Reconcile(ctx context.Context, interval, age time.Duration, workers int) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.ReconcileOnce(ctx, age, workers)
	}
}

// ReconcileOnce runs one reconciler pass and returns how many orders it tried.
func (s *Server) ReconcileOnce(ctx context.Context, age time.Duration, workers int) int {
	orders, err := s.Store.ClaimStale(ctx, age, 50)
	if err != nil {
		slog.Warn("reconciler: claim pending orders", "err", err)
		return 0
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for _, o := range orders {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer func() { <-sem; wg.Done() }()
			s.Settle(ctx, o)
		}()
	}
	wg.Wait()
	return len(orders)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
