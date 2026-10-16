// Command checkout takes orders and gets them paid through payments.
package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/zugzwang-io/kazu/examples/shop/internal/db"
	"github.com/zugzwang-io/kazu/examples/shop/internal/svc"
	"github.com/zugzwang-io/kazu/examples/shop/services/checkout"
)

func main() {
	ctx := context.Background()
	cfg := db.ServiceConfig(svc.Env("DATABASE_URL", "postgres://postgres:dev@postgres:5432/shop"), "checkout")
	cfg.PoolSize = svc.EnvInt("DB_POOL_SIZE", cfg.PoolSize)
	cfg.AcquireTimeout = svc.EnvDuration("DB_ACQUIRE_TIMEOUT", cfg.AcquireTimeout)
	cfg.StatementTimeout = svc.EnvDuration("DB_STATEMENT_TIMEOUT", cfg.StatementTimeout)
	d, err := db.Open(ctx, cfg)
	if err != nil {
		log.Fatalf("database config: %v", err)
	}
	s := &checkout.Server{
		Store: checkout.PGStore{DB: d},
		Pay: &checkout.PaymentsClient{
			URL:            svc.Env("PAYMENTS_URL", "http://payments:8081"),
			HTTP:           &http.Client{},
			AttemptTimeout: svc.EnvDuration("PAYMENTS_ATTEMPT_TIMEOUT", time.Second),
			MaxAttempts:    svc.EnvInt("PAYMENTS_MAX_ATTEMPTS", 3),
			BaseBackoff:    svc.EnvDuration("PAYMENTS_BACKOFF", 100*time.Millisecond),
		},
		Ready: d.Ping,
	}
	go s.Reconcile(ctx,
		svc.EnvDuration("RECONCILE_INTERVAL", 5*time.Second),
		svc.EnvDuration("RECONCILE_AFTER", 10*time.Second),
		svc.EnvInt("RECONCILE_WORKERS", 10))
	log.Fatal(http.ListenAndServe(svc.Env("ADDR", ":8080"), s.Handler()))
}
