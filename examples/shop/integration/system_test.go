// Package integration tests checkout, payments and fakestripe together:
// real servers over real HTTP, against a real Postgres (dbtest), with nothing
// mocked. Each test covers one end-to-end path an order can take, including
// every way a dependency can fail during a single request.
package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zugzwang-io/kazu/examples/shop/internal/db"
	"github.com/zugzwang-io/kazu/examples/shop/internal/db/dbtest"
	"github.com/zugzwang-io/kazu/examples/shop/services/checkout"
	"github.com/zugzwang-io/kazu/examples/shop/services/fakestripe"
	"github.com/zugzwang-io/kazu/examples/shop/services/payments"
)

// config tweaks one part of the shop for a test.
type config struct {
	attemptTimeout time.Duration // checkout's per-attempt timeout to payments
	maxInFlight    int           // payments' load-shedding limit
}

var defaults = config{attemptTimeout: time.Second, maxInFlight: 20}

// shop is the three services wired together:
// test → checkout → payments → fakestripe, with Postgres behind both.
type shop struct {
	db          *db.DB
	stripe      *fakestripe.Server
	stripeDown  atomic.Bool // when set, the processor answers 500 to everything
	paymentsURL string
	checkout    *checkout.Server
	url         string // checkout's URL
}

func start(t *testing.T, cfg config) *shop {
	t.Helper()
	s := &shop{
		db:     dbtest.Open(t, "payments", "checkout"),
		stripe: &fakestripe.Server{Latency: 50 * time.Millisecond, Jitter: 10 * time.Millisecond},
	}

	stripe := s.stripe.Handler()
	stripeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.stripeDown.Load() {
			http.Error(w, "processor unavailable", http.StatusInternalServerError)
			return
		}
		stripe.ServeHTTP(w, r)
	}))
	t.Cleanup(stripeSrv.Close)

	proc := &payments.StripeClient{URL: stripeSrv.URL, HTTP: &http.Client{}, Timeout: 2 * time.Second}
	paymentsSrv := httptest.NewServer(payments.NewServer(payments.PGStore{DB: s.db}, proc, cfg.maxInFlight, s.db.Ping).Handler())
	t.Cleanup(paymentsSrv.Close)
	s.paymentsURL = paymentsSrv.URL

	s.checkout, s.url = s.newCheckout(t, s.paymentsURL, cfg.attemptTimeout)
	return s
}

// newCheckout starts a checkout server calling payments at paymentsURL.
func (s *shop) newCheckout(t *testing.T, paymentsURL string, attemptTimeout time.Duration) (*checkout.Server, string) {
	t.Helper()
	c := &checkout.Server{
		Store: checkout.PGStore{DB: s.db},
		Pay: &checkout.PaymentsClient{URL: paymentsURL, HTTP: &http.Client{},
			AttemptTimeout: attemptTimeout, MaxAttempts: 3, BaseBackoff: 10 * time.Millisecond},
	}
	srv := httptest.NewServer(c.Handler())
	t.Cleanup(srv.Close)
	return c, srv.URL
}

// order POSTs an order to the checkout at url and fails the test on any
// transport error or non-2xx status.
func order(t *testing.T, url, key string, amount int) (int, checkout.Order) {
	t.Helper()
	code, o, err := tryOrder(url, key, amount)
	if err != nil {
		t.Fatal(err)
	}
	return code, o
}

func tryOrder(url, key string, amount int) (int, checkout.Order, error) {
	var o checkout.Order
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url+"/orders", strings.NewReader(`{"amount_cents":`+strconv.Itoa(amount)+`}`))
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

// get reads an order back through checkout's API.
func get(t *testing.T, url, id string) checkout.Order {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url+"/orders/"+id, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var o checkout.Order
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&o) != nil {
		t.Fatalf("GET /orders/%s: %s", id, resp.Status)
	}
	return o
}

// charges counts the charges recorded for an order.
func (s *shop) charges(t *testing.T, orderID string) int {
	t.Helper()
	var n int
	if err := s.db.Pool.QueryRow(context.Background(), `select count(*) from payments.charges where order_id = $1`, orderID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// want checks an order's final state everywhere it shows up: checkout's API,
// payments' records, and the processor.
func (s *shop) want(t *testing.T, id, status string, charges, processorCharges int) {
	t.Helper()
	if o := get(t, s.url, id); o.Status != status {
		t.Errorf("order %s is %q, want %q", id, o.Status, status)
	}
	if n := s.charges(t, id); n != charges {
		t.Errorf("order %s has %d charge rows, want %d", id, n, charges)
	}
	if n := s.stripe.Count(); n != processorCharges {
		t.Errorf("processor charged %d times, want %d", n, processorCharges)
	}
}

func TestOrderIsPaidWithOneCharge(t *testing.T) {
	s := start(t, defaults)
	code, o := order(t, s.url, "k1", 1500)
	if code != http.StatusOK || o.Status != checkout.StatusPaid || o.AmountCents != 1500 {
		t.Fatalf("got %d %+v", code, o)
	}
	s.want(t, o.ID, checkout.StatusPaid, 2, 1) // CANARY: wrong on purpose
}

func TestRepeatedKeyReturnsSameOrder(t *testing.T) {
	s := start(t, defaults)
	_, first := order(t, s.url, "k1", 1500)
	_, again := order(t, s.url, "k1", 1500)
	if first != again {
		t.Fatalf("first %+v, again %+v", first, again)
	}
	s.want(t, first.ID, checkout.StatusPaid, 1, 1)
}

func TestConcurrentDuplicateSubmitsChargeOnce(t *testing.T) {
	s := start(t, defaults)
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
				_, o, err := tryOrder(s.url, key, 2500)
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
	if n := s.stripe.Count(); n != 10 {
		t.Fatalf("processor charged %d times, want 10", n)
	}
}

// Payments isn't listening: every attempt is refused, so nothing can have
// been charged, and the order fails.
func TestPaymentsUnreachableFailsOrderWithoutCharge(t *testing.T) {
	s := start(t, defaults)
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close() // a URL where nothing listens
	_, url := s.newCheckout(t, gone.URL, time.Second)

	_, o := order(t, url, "k1", 1500)
	if o.Status != checkout.StatusFailed {
		t.Fatalf("got %q, want failed", o.Status)
	}
	s.want(t, o.ID, checkout.StatusFailed, 0, 0)
}

// Payments sheds every attempt with 503 before doing any work, so the order
// fails without a charge.
func TestPaymentsOverloadedFailsOrderWithoutCharge(t *testing.T) {
	s := start(t, config{attemptTimeout: time.Second, maxInFlight: 0}) // sheds everything
	_, o := order(t, s.url, "k1", 1500)
	if o.Status != checkout.StatusFailed {
		t.Fatalf("got %q, want failed", o.Status)
	}
	s.want(t, o.ID, checkout.StatusFailed, 0, 0)
}

// Checkout gives up before payments answers. The abandoned call still
// charges at the processor, so the outcome is unknown: the order stays
// pending, and the reconciler finishes it with the same key and one charge.
func TestTimedOutOrderIsReconciledWithOneCharge(t *testing.T) {
	s := start(t, config{attemptTimeout: 20 * time.Millisecond, maxInFlight: 20}) // < fakestripe's 40–60 ms
	code, o := order(t, s.url, "slow", 1500)
	if code != http.StatusAccepted || o.Status != checkout.StatusPending {
		t.Fatalf("got %d %+v, want 202 pending", code, o)
	}
	if n := s.stripe.Count(); n != 1 {
		t.Fatalf("processor charged %d times, want 1 (the abandoned call still charges)", n)
	}

	patient, _ := s.newCheckout(t, s.paymentsURL, time.Second)
	if n := patient.ReconcileOnce(context.Background(), 0, 2); n != 1 {
		t.Fatalf("reconciler tried %d orders, want 1", n)
	}
	s.want(t, o.ID, checkout.StatusPaid, 1, 1)
}

// The processor is down: payments answers 502, which checkout can't tell
// apart from a charge that happened, so the order stays pending. Once the
// processor is back, the reconciler pays it with one charge.
func TestProcessorOutageLeavesOrderPendingUntilReconciled(t *testing.T) {
	s := start(t, defaults)
	s.stripeDown.Store(true)
	code, o := order(t, s.url, "k1", 1500)
	if code != http.StatusAccepted || o.Status != checkout.StatusPending {
		t.Fatalf("got %d %+v, want 202 pending", code, o)
	}
	s.want(t, o.ID, checkout.StatusPending, 0, 0)

	s.stripeDown.Store(false)
	if n := s.checkout.ReconcileOnce(context.Background(), 0, 2); n != 1 {
		t.Fatalf("reconciler tried %d orders, want 1", n)
	}
	s.want(t, o.ID, checkout.StatusPaid, 1, 1)
}

// A pending order is resumed by the client's retry and the reconciler at the
// same moment: both settle it with the same key, so it's charged once.
func TestClientRetryRacingReconcilerChargesOnce(t *testing.T) {
	s := start(t, config{attemptTimeout: 20 * time.Millisecond, maxInFlight: 20})
	_, o := order(t, s.url, "k1", 1500)
	if o.Status != checkout.StatusPending {
		t.Fatalf("got %q, want pending", o.Status)
	}

	patient, patientURL := s.newCheckout(t, s.paymentsURL, time.Second)
	fire := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); <-fire; patient.ReconcileOnce(context.Background(), 0, 2) }()
	go func() {
		defer wg.Done()
		<-fire
		if _, _, err := tryOrder(patientURL, "k1", 1500); err != nil {
			t.Error(err)
		}
	}()
	close(fire)
	wg.Wait()
	s.want(t, o.ID, checkout.StatusPaid, 1, 1)
}
