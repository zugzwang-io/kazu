package payments

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zugzwang-io/kazu/examples/shop/services/fakestripe"
)

func TestStripeClient(t *testing.T) {
	srv := httptest.NewServer((&fakestripe.Server{}).Handler())
	defer srv.Close()
	c := &StripeClient{URL: srv.URL, HTTP: srv.Client(), Timeout: time.Second}

	a, err := c.Charge(context.Background(), "k", 1500)
	if err != nil || a == "" {
		t.Fatalf("charge: %q %v", a, err)
	}
	if b, _ := c.Charge(context.Background(), "k", 1500); b != a {
		t.Fatalf("same key gave %q then %q", a, b)
	}
}

func TestStripeClientErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Idempotency-Key") == "slow" {
			time.Sleep(200 * time.Millisecond)
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := &StripeClient{URL: srv.URL, HTTP: srv.Client(), Timeout: 50 * time.Millisecond}
	if _, err := c.Charge(context.Background(), "k", 1); err == nil {
		t.Fatal("a 503 was not an error")
	}
	if _, err := c.Charge(context.Background(), "slow", 1); err == nil {
		t.Fatal("a timeout was not an error")
	}
}
