// Package integration runs checkout, payments and fakestripe in-process
// against a real Postgres. It needs SHOP_TEST_DATABASE_URL, a database it may
// wipe; without it the tests are skipped.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zugzwang-io/kazu/examples/shop/internal/svc"
	"github.com/zugzwang-io/kazu/examples/shop/services/checkout"
	"github.com/zugzwang-io/kazu/examples/shop/services/fakestripe"
	"github.com/zugzwang-io/kazu/examples/shop/services/payments"
)

type shop struct {
	db       *svc.DB
	stripe   *fakestripe.Server
	payments *httptest.Server
	checkout *checkout.Server
	url      string
}

func start(t *testing.T, attemptTimeout time.Duration) *shop {
	t.Helper()
	url := os.Getenv("SHOP_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("SHOP_TEST_DATABASE_URL not set")
	}
	t.Setenv("DATABASE_URL", url)
	ctx := context.Background()
	db, err := svc.OpenDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Pool.Close)
	schema, err := os.ReadFile("../db/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, "drop table if exists orders, charges cascade;\n"+string(schema)); err != nil {
		t.Fatal(err)
	}

	s := &shop{db: db, stripe: &fakestripe.Server{Latency: 50 * time.Millisecond, Jitter: 10 * time.Millisecond}}
	stripeSrv := httptest.NewServer(s.stripe.Handler())
	t.Cleanup(stripeSrv.Close)
	proc := &payments.StripeClient{URL: stripeSrv.URL, HTTP: &http.Client{}, Timeout: 2 * time.Second}
	s.payments = httptest.NewServer(payments.NewServer(payments.PGStore{DB: db}, proc, 20, db.Ping).Handler())
	t.Cleanup(s.payments.Close)
	s.checkout = s.newCheckout(attemptTimeout)
	srv := httptest.NewServer(s.checkout.Handler())
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

func (s *shop) newCheckout(attemptTimeout time.Duration) *checkout.Server {
	return &checkout.Server{
		Store: checkout.PGStore{DB: s.db},
		Pay: &checkout.PaymentsClient{URL: s.payments.URL, HTTP: &http.Client{},
			AttemptTimeout: attemptTimeout, MaxAttempts: 3, BaseBackoff: 10 * time.Millisecond},
	}
}

func (s *shop) order(t *testing.T, key string, amount int) (int, checkout.Order) {
	t.Helper()
	code, o, err := s.tryOrder(key, amount)
	if err != nil {
		t.Fatal(err)
	}
	return code, o
}

func (s *shop) tryOrder(key string, amount int) (int, checkout.Order, error) {
	var o checkout.Order
	req, _ := http.NewRequest(http.MethodPost, s.url+"/orders", strings.NewReader(`{"amount_cents":`+strconv.Itoa(amount)+`}`))
	req.Header.Set("Idempotency-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, o, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return resp.StatusCode, o, fmt.Errorf("POST /orders %s: %s", key, resp.Status)
	}
	return resp.StatusCode, o, json.NewDecoder(resp.Body).Decode(&o)
}

func (s *shop) charges(t *testing.T, orderID string) int {
	t.Helper()
	var n int
	if err := s.db.Pool.QueryRow(context.Background(), `select count(*) from charges where order_id = $1`, orderID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOrderIsPaidWithOneCharge(t *testing.T) {
	s := start(t, time.Second)
	code, o := s.order(t, "k1", 1500)
	if code != http.StatusOK || o.Status != checkout.StatusPaid {
		t.Fatalf("got %d %+v", code, o)
	}
	if n := s.charges(t, o.ID); n != 1 {
		t.Fatalf("%d charges, want 1", n)
	}
	resp, err := http.Get(s.url + "/orders/" + o.ID)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("GET order: %v %v", err, resp)
	}
	resp.Body.Close()
}

func TestRepeatedKeyReturnsSameOrder(t *testing.T) {
	s := start(t, time.Second)
	_, first := s.order(t, "k1", 1500)
	_, again := s.order(t, "k1", 1500)
	if first != again {
		t.Fatalf("first %+v, again %+v", first, again)
	}
	if n := s.charges(t, first.ID); n != 1 {
		t.Fatalf("%d charges, want 1", n)
	}
}

func TestConcurrentDuplicateSubmitsChargeOnce(t *testing.T) {
	s := start(t, time.Second)
	for k := range 10 {
		key := fmt.Sprintf("dup-%d", k)
		fire := make(chan struct{})
		errs := make(chan error, 10)
		var wg sync.WaitGroup
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-fire // all ten submit at once
				_, o, err := s.tryOrder(key, 2500)
				if err == nil && o.ID != checkout.OrderID(key) {
					err = fmt.Errorf("got order %q", o.ID)
				}
				errs <- err
			}()
		}
		close(fire)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if n := s.charges(t, checkout.OrderID(key)); n != 1 {
			t.Fatalf("%s: %d charges, want 1", key, n)
		}
	}
}

// Checkout gives up before payments answers: the outcome is unknown, so the
// order stays pending, and the reconciler finishes it with the same key.
func TestTimedOutOrderIsReconciledWithoutDoubleCharge(t *testing.T) {
	s := start(t, 20*time.Millisecond) // shorter than fakestripe's 40–60 ms
	code, o := s.order(t, "slow", 1500)
	if code != http.StatusAccepted || o.Status != checkout.StatusPending {
		t.Fatalf("got %d %+v, want 202 pending", code, o)
	}
	if s.stripe.Count() != 1 {
		t.Fatalf("processor charged %d times, want 1 (the abandoned call still charges)", s.stripe.Count())
	}

	patient := s.newCheckout(time.Second)
	if n := patient.ReconcileOnce(context.Background(), 0, 2); n != 1 {
		t.Fatalf("reconciler tried %d orders, want 1", n)
	}
	got, err := checkout.PGStore{DB: s.db}.Get(context.Background(), o.ID)
	if err != nil || got.Status != checkout.StatusPaid {
		t.Fatalf("after reconcile: %+v %v", got, err)
	}
	if n := s.charges(t, o.ID); n != 1 {
		t.Fatalf("%d charges, want 1", n)
	}
	if s.stripe.Count() != 1 {
		t.Fatalf("processor charged %d times, want 1", s.stripe.Count())
	}
}
