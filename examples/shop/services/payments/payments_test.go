package payments

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type memStore struct {
	mu      sync.Mutex
	charges map[string]Charge
}

func (m *memStore) Find(_ context.Context, key string) (Charge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.charges[key]
	if !ok {
		return c, ErrNotFound
	}
	return c, nil
}

func (m *memStore) InsertOnce(_ context.Context, c Charge) (Charge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.charges == nil {
		m.charges = map[string]Charge{}
	}
	if existing, ok := m.charges[c.IdempotencyKey]; ok {
		return existing, nil
	}
	m.charges[c.IdempotencyKey] = c
	return c, nil
}

type fakeProcessor struct {
	mu    sync.Mutex
	calls int
	err   error
	block chan struct{} // if set, Charge waits on it
}

func (p *fakeProcessor) Charge(ctx context.Context, key string, _ int64) (string, error) {
	p.mu.Lock()
	p.calls++
	block, err := p.block, p.err
	p.mu.Unlock()
	if block != nil {
		<-block
	}
	return "ch_" + key, err
}

func charge(h http.Handler, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/charges", strings.NewReader(body))
	req.Header.Set("Idempotency-Key", key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const body = `{"order_id": "ord_k", "amount_cents": 1500}`

func TestChargeOncePerKey(t *testing.T) {
	store, proc := &memStore{}, &fakeProcessor{}
	h := NewServer(store, proc, 20, nil).Handler()
	for range 3 {
		if rec := charge(h, "k", body); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"ch_k"`) {
			t.Fatalf("got %d %s", rec.Code, rec.Body)
		}
	}
	if proc.calls != 1 || len(store.charges) != 1 {
		t.Fatalf("%d processor calls, %d charges; want 1 and 1", proc.calls, len(store.charges))
	}
}

func TestProcessorFailureRecordsNothing(t *testing.T) {
	store := &memStore{}
	h := NewServer(store, &fakeProcessor{err: errors.New("boom")}, 20, nil).Handler()
	if rec := charge(h, "k", body); rec.Code != http.StatusBadGateway {
		t.Fatalf("got %d, want 502", rec.Code)
	}
	if len(store.charges) != 0 {
		t.Fatal("recorded a charge the processor didn't make")
	}
}

func TestShedsBeyondLimit(t *testing.T) {
	proc := &fakeProcessor{block: make(chan struct{})}
	h := NewServer(&memStore{}, proc, 1, nil).Handler()
	done := make(chan int)
	go func() { done <- charge(h, "a", body).Code }()
	for { // wait until the first request holds the only slot
		proc.mu.Lock()
		n := proc.calls
		proc.mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if code := charge(h, "b", body).Code; code != http.StatusServiceUnavailable {
		t.Fatalf("second concurrent request got %d, want 503", code)
	}
	close(proc.block)
	if code := <-done; code != http.StatusOK {
		t.Fatalf("first request got %d", code)
	}
	if code := charge(h, "c", body).Code; code != http.StatusOK {
		t.Fatalf("after the slot freed up, got %d", code)
	}
}

func TestRejectsBadRequests(t *testing.T) {
	h := NewServer(&memStore{}, &fakeProcessor{}, 20, nil).Handler()
	for _, c := range []struct{ key, body string }{
		{"", body},
		{"k", `{"amount_cents": 5}`},
		{"k", `{"order_id": "o", "amount_cents": 0}`},
	} {
		if code := charge(h, c.key, c.body).Code; code != http.StatusBadRequest {
			t.Errorf("%q %s: got %d, want 400", c.key, c.body, code)
		}
	}
}
