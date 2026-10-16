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
	cfg := db.ServiceConfig(svc.Env("DATABASE_URL", "postgres://postgres:dev@postgres:5432/shop"), "payments")
	cfg.PoolSize = svc.EnvInt("DB_POOL_SIZE", cfg.PoolSize)
	cfg.AcquireTimeout = svc.EnvDuration("DB_ACQUIRE_TIMEOUT", cfg.AcquireTimeout)
	cfg.StatementTimeout = svc.EnvDuration("DB_STATEMENT_TIMEOUT", cfg.StatementTimeout)
	d, err := db.Open(context.Background(), cfg)
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
