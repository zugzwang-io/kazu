// Package svc holds the plumbing every shop service shares: env-var config,
// the database pool, serving with graceful shutdown, and the healthcheck command.
package svc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Env returns the env var name, or def when it is unset or empty.
func Env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// EnvInt is Env for integers; a malformed value is a startup error.
func EnvInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		Fatal("bad integer env var", "name", name, "value", v)
	}
	return n
}

// EnvDuration is Env for durations such as "500ms".
func EnvDuration(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		Fatal("bad duration env var", "name", name, "value", v)
	}
	return d
}

func Fatal(msg string, args ...any) {
	slog.Error(msg, args...)
	os.Exit(1)
}

// DB is a pgx pool plus the acquire timeout every query waits at most:
// pgxpool has no acquire timeout of its own, and without one a drained pool
// makes requests wait for their whole deadline instead of failing fast.
type DB struct {
	Pool           *pgxpool.Pool
	AcquireTimeout time.Duration
}

// OpenDB builds the pool from DATABASE_URL, DB_POOL_SIZE (10),
// DB_ACQUIRE_TIMEOUT (500ms) and DB_STATEMENT_TIMEOUT (2s).
// The pool connects lazily and redials after failures.
func OpenDB(ctx context.Context) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(Env("DATABASE_URL", "postgres://postgres:dev@postgres:5432/shop"))
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = int32(EnvInt("DB_POOL_SIZE", 10))
	cfg.HealthCheckPeriod = time.Second
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = strconv.Itoa(int(EnvDuration("DB_STATEMENT_TIMEOUT", 2*time.Second).Milliseconds()))
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &DB{Pool: pool, AcquireTimeout: EnvDuration("DB_ACQUIRE_TIMEOUT", 500*time.Millisecond)}, nil
}

// Acquire takes a connection, waiting at most AcquireTimeout for one.
// The connection itself stays bound to ctx, the request's deadline.
func (db *DB) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	actx, cancel := context.WithTimeout(ctx, db.AcquireTimeout)
	defer cancel()
	c, err := db.Pool.Acquire(actx)
	if err != nil {
		return nil, fmt.Errorf("acquire db connection: %w", err)
	}
	return c, nil
}

// Ping is what /healthz reports.
func (db *DB) Ping(ctx context.Context) error {
	c, err := db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer c.Release()
	return c.Ping(ctx)
}

// Serve runs h on addr until SIGTERM or SIGINT, then drains for up to 5 s.
// If certFile is set it serves TLS.
func Serve(addr string, h http.Handler, certFile, keyFile string) {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second}
	errc := make(chan error, 1)
	go func() {
		if certFile != "" {
			errc <- srv.ListenAndServeTLS(certFile, keyFile)
		} else {
			errc <- srv.ListenAndServe()
		}
	}()
	slog.Info("listening", "addr", addr, "tls", certFile != "")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	select {
	case err := <-errc:
		Fatal("server stopped", "err", err)
	case <-stop:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// HTTPClient returns a client that trusts only the CA in caFile: the sealed
// system has no public endpoints. An empty caFile gives a plain client.
func HTTPClient(caFile string) (*http.Client, error) {
	if caFile == "" {
		return &http.Client{}, nil
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("no certificates in " + caFile)
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	t.MaxIdleConnsPerHost = 64
	return &http.Client{Transport: t}, nil
}

// Healthcheck GETs url and exits 0 on a 2xx: the compose healthcheck for
// images that have no curl. CA_FILE is honoured for HTTPS.
func Healthcheck(url string) {
	c, err := HTTPClient(os.Getenv("CA_FILE"))
	if err != nil {
		Fatal("healthcheck", "err", err)
	}
	c.Timeout = 2 * time.Second
	resp, err := c.Get(url)
	if err != nil {
		Fatal("healthcheck", "err", err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		Fatal("healthcheck", "status", resp.StatusCode)
	}
}

// HealthzHandler answers 200 once the database is reachable.
func HealthzHandler(db *DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := db.Ping(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
