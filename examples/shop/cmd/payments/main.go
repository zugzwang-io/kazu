// Command payments charges orders through the card processor.
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/zugzwang-io/kazu/examples/shop/internal/db"
	"github.com/zugzwang-io/kazu/examples/shop/internal/svc"
	"github.com/zugzwang-io/kazu/examples/shop/services/payments"
)

func main() {
	d, err := db.Open(context.Background(), db.Config{
		URL:              svc.Env("DATABASE_URL", "postgres://postgres:dev@postgres:5432/shop"),
		PoolSize:         svc.EnvInt("DB_POOL_SIZE", 10),
		AcquireTimeout:   svc.EnvDuration("DB_ACQUIRE_TIMEOUT", 500*time.Millisecond),
		StatementTimeout: svc.EnvDuration("DB_STATEMENT_TIMEOUT", 2*time.Second),
	})
	if err != nil {
		log.Fatalf("database config: %v", err)
	}
	stripe := &payments.StripeClient{
		URL:     svc.Env("STRIPE_URL", "http://fakestripe:12111"),
		HTTP:    &http.Client{},
		Timeout: svc.EnvDuration("STRIPE_TIMEOUT", 2*time.Second),
	}
	s := payments.NewServer(payments.PGStore{DB: d}, stripe, svc.EnvInt("MAX_IN_FLIGHT", 20), d.Ping)
	log.Fatal(http.ListenAndServe(svc.Env("ADDR", ":8081"), s.Handler()))
}
