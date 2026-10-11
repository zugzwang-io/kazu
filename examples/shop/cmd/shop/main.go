// Command shop runs one of the shop's services. One binary and one image for
// all of them keeps base and head to a single tag each.
//
//	shop checkout | payments | fakestripe
//	shop healthcheck <url>
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/zugzwang-io/kazu/examples/shop/internal/svc"
	"github.com/zugzwang-io/kazu/examples/shop/services/checkout"
	"github.com/zugzwang-io/kazu/examples/shop/services/fakestripe"
	"github.com/zugzwang-io/kazu/examples/shop/services/payments"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: shop checkout|payments|fakestripe|healthcheck <url>")
		os.Exit(2)
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("service", os.Args[1]))
	ctx := context.Background()

	switch os.Args[1] {
	case "checkout":
		db := openDB(ctx)
		client, err := svc.HTTPClient("")
		if err != nil {
			svc.Fatal("http client", "err", err)
		}
		s := &checkout.Server{
			Store: checkout.PGStore{DB: db},
			Pay: &checkout.PaymentsClient{
				URL:            svc.Env("PAYMENTS_URL", "http://payments:8081"),
				HTTP:           client,
				AttemptTimeout: svc.EnvDuration("PAYMENTS_ATTEMPT_TIMEOUT", time.Second),
				MaxAttempts:    svc.EnvInt("PAYMENTS_MAX_ATTEMPTS", 3),
				BaseBackoff:    svc.EnvDuration("PAYMENTS_BACKOFF", 100*time.Millisecond),
			},
			Ready: db.Ping,
		}
		go s.Reconcile(ctx,
			svc.EnvDuration("RECONCILE_INTERVAL", 5*time.Second),
			svc.EnvDuration("RECONCILE_AFTER", 10*time.Second),
			svc.EnvInt("RECONCILE_WORKERS", 10))
		svc.Serve(svc.Env("ADDR", ":8080"), s.Handler(), "", "")

	case "payments":
		db := openDB(ctx)
		client, err := svc.HTTPClient(svc.Env("CA_FILE", "/certs/ca.pem"))
		if err != nil {
			svc.Fatal("http client", "err", err)
		}
		stripe := &payments.StripeClient{
			URL:     svc.Env("STRIPE_URL", "https://fakestripe:8443"),
			HTTP:    client,
			Timeout: svc.EnvDuration("STRIPE_TIMEOUT", 2*time.Second),
		}
		s := payments.NewServer(payments.PGStore{DB: db}, stripe, svc.EnvInt("MAX_IN_FLIGHT", 20), db.Ping)
		svc.Serve(svc.Env("ADDR", ":8081"), s.Handler(), "", "")

	case "fakestripe":
		s := &fakestripe.Server{
			Latency: svc.EnvDuration("LATENCY", 200*time.Millisecond),
			Jitter:  svc.EnvDuration("JITTER", 50*time.Millisecond),
		}
		svc.Serve(svc.Env("ADDR", ":8443"), s.Handler(),
			svc.Env("TLS_CERT", "/certs/fakestripe.pem"), svc.Env("TLS_KEY", "/certs/fakestripe-key.pem"))

	case "healthcheck":
		if len(os.Args) != 3 {
			svc.Fatal("usage: shop healthcheck <url>")
		}
		svc.Healthcheck(os.Args[2])

	default:
		svc.Fatal("unknown service", "name", os.Args[1])
	}
}

func openDB(ctx context.Context) *svc.DB {
	db, err := svc.OpenDB(ctx)
	if err != nil {
		svc.Fatal("database config", "err", err)
	}
	return db
}
