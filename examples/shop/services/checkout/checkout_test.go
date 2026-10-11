package checkout

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// memStore is Store in memory.
type memStore struct {
	mu      sync.Mutex
	orders  map[string]Order
	touched map[string]time.Time
}

func newMemStore() *memStore {
	return &memStore{orders: map[string]Order{}, touched: map[string]time.Time{}}
}

func (m *memStore) CreateOrGet(_ context.Context, key string, amount int64) (Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := OrderID(key)
	if o, ok := m.orders[id]; ok {
		return o, nil
	}
	o := Order{ID: id, IdempotencyKey: key, AmountCents: amount, Status: StatusPending}
	m.orders[id], m.touched[id] = o, time.Now()
	return o, nil
}

func (m *memStore) Resolve(_ context.Context, id, status string) (Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[id]
	if !ok {
		return o, ErrNotFound
	}
	if o.Status == StatusPending {
		o.Status = status
		m.orders[id] = o
	}
	return o, nil
}

func (m *memStore) Get(_ context.Context, id string) (Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[id]
	if !ok {
		return o, ErrNotFound
	}
	return o, nil
}

func (m *memStore) List(_ context.Context, status string, limit int) ([]Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Order
	for _, o := range m.orders {
		if status == "" || o.Status == status {
			out = append(out, o)
		}
	}
	return out, nil
}

func (m *memStore) ClaimStale(_ context.Context, age time.Duration, limit int) ([]Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Order
	for id, o := range m.orders {
		if o.Status == StatusPending && time.Since(m.touched[id]) >= age && len(out) < limit {
			m.touched[id] = time.Now()
			out = append(out, o)
		}
	}
	return out, nil
}

// fakePayer returns a fixed outcome and records the orders it was asked to charge.
type fakePayer struct {
	mu      sync.Mutex
	outcome Outcome
	calls   []Order
}

func (f *fakePayer) Charge(_ context.Context, o Order) Outcome {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, o)
	return f.outcome
}

func (f *fakePayer) set(o Outcome) { f.mu.Lock(); f.outcome = o; f.mu.Unlock() }
func (f *fakePayer) count() int    { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

func post(t *testing.T, h http.Handler, key, body string) (*httptest.ResponseRecorder, Order) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/orders", strings.NewReader(body))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var o Order
	_ = json.Unmarshal(rec.Body.Bytes(), &o)
	return rec, o
}

func TestCreateOrderOutcomes(t *testing.T) {
	cases := []struct {
		outcome    Outcome
		wantStatus string
		wantCode   int
	}{
		{OutcomePaid, StatusPaid, http.StatusOK},
		{OutcomeDeclined, StatusFailed, http.StatusOK},
		{OutcomeUnknown, StatusPending, http.StatusAccepted},
	}
	for _, c := range cases {
		t.Run(c.wantStatus, func(t *testing.T) {
			pay := &fakePayer{outcome: c.outcome}
			s := &Server{Store: newMemStore(), Pay: pay}
			rec, o := post(t, s.Handler(), "k1", `{"amount_cents": 1500}`)
			if rec.Code != c.wantCode || o.Status != c.wantStatus {
				t.Fatalf("got %d %q, want %d %q", rec.Code, o.Status, c.wantCode, c.wantStatus)
			}
			if o.ID != "ord_k1" || o.AmountCents != 1500 || pay.count() != 1 {
				t.Fatalf("order %+v, %d charge calls", o, pay.count())
			}
		})
	}
}

func TestRepeatedKeyReturnsExistingOrder(t *testing.T) {
	pay := &fakePayer{outcome: OutcomePaid}
	s := &Server{Store: newMemStore(), Pay: pay}
	_, first := post(t, s.Handler(), "k1", `{"amount_cents": 1500}`)
	_, again := post(t, s.Handler(), "k1", `{"amount_cents": 1500}`)
	if first != again || again.Status != StatusPaid {
		t.Fatalf("first %+v, again %+v", first, again)
	}
	if pay.count() != 1 {
		t.Fatalf("a paid order was charged again: %d calls", pay.count())
	}
}

func TestRepeatedKeyResumesPendingOrder(t *testing.T) {
	pay := &fakePayer{outcome: OutcomeUnknown}
	s := &Server{Store: newMemStore(), Pay: pay}
	_, first := post(t, s.Handler(), "k1", `{"amount_cents": 1500}`)
	pay.set(OutcomePaid)
	_, again := post(t, s.Handler(), "k1", `{"amount_cents": 1500}`)
	if first.Status != StatusPending || again.Status != StatusPaid || pay.count() != 2 {
		t.Fatalf("first %q, again %q, %d calls", first.Status, again.Status, pay.count())
	}
	if pay.calls[0].IdempotencyKey != pay.calls[1].IdempotencyKey {
		t.Fatal("resumed with a different key")
	}
}

func TestCreateOrderRejectsBadRequests(t *testing.T) {
	s := &Server{Store: newMemStore(), Pay: &fakePayer{}}
	for name, c := range map[string]struct{ key, body string }{
		"no key":          {"", `{"amount_cents": 1}`},
		"long key":        {strings.Repeat("k", 201), `{"amount_cents": 1}`},
		"no amount":       {"k", `{}`},
		"negative amount": {"k", `{"amount_cents": -5}`},
		"not json":        {"k", `amount=5`},
	} {
		if rec, _ := post(t, s.Handler(), c.key, c.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, rec.Code)
		}
	}
}

func TestReconcilerResolvesStalePendingOrder(t *testing.T) {
	store, pay := newMemStore(), &fakePayer{outcome: OutcomeUnknown}
	s := &Server{Store: store, Pay: pay}
	post(t, s.Handler(), "k1", `{"amount_cents": 1500}`)

	pay.set(OutcomePaid)
	if n := s.ReconcileOnce(context.Background(), time.Hour, 2); n != 0 {
		t.Fatalf("reconciler took an order younger than its age threshold: %d", n)
	}
	if n := s.ReconcileOnce(context.Background(), 0, 2); n != 1 {
		t.Fatalf("reconciler tried %d orders, want 1", n)
	}
	o, _ := store.Get(context.Background(), "ord_k1")
	if o.Status != StatusPaid {
		t.Fatalf("status %q, want paid", o.Status)
	}
	if pay.calls[1].IdempotencyKey != "k1" {
		t.Fatalf("reconciler used key %q, want the original", pay.calls[1].IdempotencyKey)
	}
}
