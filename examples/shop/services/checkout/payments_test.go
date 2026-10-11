package checkout

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// scripted serves one response per attempt from codes; 0 means "hang past the
// attempt timeout". It records each attempt's idempotency key.
type scripted struct {
	mu    sync.Mutex
	codes []int
	keys  []string
}

func (s *scripted) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	n := len(s.keys)
	s.keys = append(s.keys, r.Header.Get("Idempotency-Key"))
	code := s.codes[min(n, len(s.codes)-1)]
	s.mu.Unlock()
	if code == 0 {
		io.Copy(io.Discard, r.Body) // lets the server notice the client hang up
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
		return
	}
	w.WriteHeader(code)
}

func (s *scripted) attempts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.keys...)
}

func client(url string) *PaymentsClient {
	return &PaymentsClient{URL: url, HTTP: &http.Client{}, AttemptTimeout: 100 * time.Millisecond, MaxAttempts: 3, BaseBackoff: time.Millisecond}
}

func TestPaymentsRetryPolicy(t *testing.T) {
	cases := []struct {
		name     string
		codes    []int
		want     Outcome
		attempts int
	}{
		{"paid first time", []int{200}, OutcomePaid, 1},
		{"4xx is final", []int{402}, OutcomeDeclined, 1},
		{"5xx retried until paid", []int{500, 502, 200}, OutcomePaid, 3},
		{"5xx every time leaves it unknown", []int{500}, OutcomeUnknown, 3},
		{"shed every time: not charged", []int{503}, OutcomeDeclined, 3},
		{"shed then timeout: unknown", []int{503, 0, 503}, OutcomeUnknown, 3},
		{"timeouts leave it unknown", []int{0}, OutcomeUnknown, 3},
		{"timeout then paid", []int{0, 200}, OutcomePaid, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &scripted{codes: c.codes}
			srv := httptest.NewServer(h)
			defer srv.Close()
			got := client(srv.URL).Charge(context.Background(), Order{ID: "ord_k", IdempotencyKey: "k", AmountCents: 100})
			keys := h.attempts()
			if got != c.want || len(keys) != c.attempts {
				t.Fatalf("got outcome %d after %d attempts, want %d after %d", got, len(keys), c.want, c.attempts)
			}
			for _, k := range keys {
				if k != "k" {
					t.Fatalf("attempt used key %q, want the order's own", k)
				}
			}
		})
	}
}

func TestPaymentsRefusedIsNotCharged(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close() // nothing listens here now: connections are refused
	if got := client("http://"+addr).Charge(context.Background(), Order{IdempotencyKey: "k"}); got != OutcomeDeclined {
		t.Fatalf("got %d, want declined", got)
	}
}

func TestPaymentsStopsWhenCallerGivesUp(t *testing.T) {
	h := &scripted{codes: []int{0}}
	srv := httptest.NewServer(h)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	c := client(srv.URL)
	c.BaseBackoff = time.Second
	if got := c.Charge(ctx, Order{IdempotencyKey: "k"}); got != OutcomeUnknown {
		t.Fatalf("got %d, want unknown", got)
	}
	if n := len(h.attempts()); n > 2 {
		t.Fatalf("kept retrying after the caller's deadline: %d attempts", n)
	}
}
