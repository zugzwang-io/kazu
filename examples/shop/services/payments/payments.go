// Package payments charges orders through the card processor (fakestripe)
// exactly once per idempotency key, and sheds load beyond a fixed concurrency.
package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type Charge struct {
	ID             string `json:"id"` // the processor's charge id
	OrderID        string `json:"order_id"`
	IdempotencyKey string `json:"idempotency_key"`
	AmountCents    int64  `json:"amount_cents"`
}

var ErrNotFound = errors.New("not found")

// Store is payments' view of the charges table.
type Store interface {
	Find(ctx context.Context, key string) (Charge, error)
	// InsertOnce records c unless a charge with its idempotency key exists, and
	// returns whichever charge is recorded. It deduplicates under a lock on the
	// key rather than a unique index.
	InsertOnce(ctx context.Context, c Charge) (Charge, error)
}

// Processor is the external card processor.
type Processor interface {
	Charge(ctx context.Context, key string, amountCents int64) (chargeID string, err error)
}

type Server struct {
	Store     Store
	Processor Processor
	Ready     func(context.Context) error
	inFlight  chan struct{}
}

// NewServer sheds requests beyond maxInFlight concurrent charges with an
// immediate 503.
func NewServer(store Store, proc Processor, maxInFlight int, ready func(context.Context) error) *Server {
	return &Server{Store: store, Processor: proc, Ready: ready, inFlight: make(chan struct{}, maxInFlight)}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /charges", s.charge)
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

func (s *Server) charge(w http.ResponseWriter, r *http.Request) {
	select {
	case s.inFlight <- struct{}{}:
		defer func() { <-s.inFlight }()
	default:
		http.Error(w, "overloaded", http.StatusServiceUnavailable)
		return
	}

	key := r.Header.Get("Idempotency-Key")
	var body struct {
		OrderID     string `json:"order_id"`
		AmountCents int64  `json:"amount_cents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || key == "" || body.OrderID == "" || body.AmountCents <= 0 {
		http.Error(w, "need Idempotency-Key and {order_id, amount_cents > 0}", http.StatusBadRequest)
		return
	}
	ctx := r.Context() // cancelled if the caller goes away, like any Go handler

	existing, err := s.Store.Find(ctx, key)
	switch {
	case err == nil:
		writeJSON(w, existing)
		return
	case !errors.Is(err, ErrNotFound):
		slog.Error("find charge", "key", key, "err", err)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}

	// The processor is idempotent on the key, so a retry after a failure below
	// gets the same charge back rather than a second one.
	chargeID, err := s.Processor.Charge(ctx, key, body.AmountCents)
	if err != nil {
		slog.Warn("processor", "key", key, "err", err)
		http.Error(w, "card processor unavailable", http.StatusBadGateway)
		return
	}
	// Money has moved: record it even if the caller has gone away meanwhile.
	c, err := s.Store.InsertOnce(context.WithoutCancel(ctx), Charge{ID: chargeID, OrderID: body.OrderID, IdempotencyKey: key, AmountCents: body.AmountCents})
	if err != nil {
		slog.Error("record charge", "key", key, "err", err)
		http.Error(w, "database unavailable", http.StatusInternalServerError)
		return
	}
	writeJSON(w, c)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// StripeClient calls the processor's POST /v1/charges.
type StripeClient struct {
	URL     string // e.g. https://fakestripe:8443
	HTTP    *http.Client
	Timeout time.Duration
}

func (c *StripeClient) Charge(ctx context.Context, key string, amountCents int64) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	body, _ := json.Marshal(map[string]int64{"amount": amountCents})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL+"/v1/charges", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("processor returned %s", resp.Status)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.ID == "" {
		return "", fmt.Errorf("processor response: %v", err)
	}
	return out.ID, nil
}
