// Package dbtest gives each test its own fresh database with the shop's
// schema, on a Postgres container started with testcontainers. Tests need
// Docker and nothing else.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"github.com/zugzwang-io/kazu/examples/shop/internal/db"
)

var (
	startOnce sync.Once
	serverURL string
	startErr  error
)

// server starts one Postgres container per test binary, i.e. per package,
// and returns a URL for its admin database. Testcontainers' reaper removes
// the container when the test process exits.
func server(t testing.TB) string {
	t.Helper()
	startOnce.Do(func() {
		ctx := context.Background()
		c, err := postgres.Run(ctx, "postgres:16",
			postgres.WithUsername("postgres"),
			postgres.WithPassword("dev"),
			postgres.WithDatabase("postgres"),
			// Postgres logs this twice: once for its init-time server, once
			// for the real one.
			testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(time.Minute)),
		)
		if err != nil {
			startErr = err
			return
		}
		serverURL, startErr = c.ConnectionString(ctx, "sslmode=disable")
	})
	if startErr != nil {
		t.Fatalf("start postgres container: %v", startErr)
	}
	return serverURL
}

// Open creates a fresh database on the package's Postgres container, applies
// the schema.sql of each named service (services/<name>/schema.sql), and
// drops the database when the test ends. The returned pool's search_path
// covers those services' schemas, so their unqualified queries work.
func Open(t testing.TB, services ...string) *db.DB {
	t.Helper()
	admin := server(t)
	ctx := context.Background()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "shop_test_" + hex.EncodeToString(b)

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "create database "+name); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close(ctx)

	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	d, err := db.Open(ctx, db.Config{URL: u.String(), Schema: strings.Join(services, ","), PoolSize: 20, AcquireTimeout: time.Second, StatementTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		d.Pool.Close()
		if c, err := pgx.Connect(ctx, admin); err == nil {
			_, _ = c.Exec(ctx, "drop database "+name+" with (force)")
			_ = c.Close(ctx)
		}
	})

	// Tests run in their own package's folder, so find the schemas relative
	// to this file instead.
	_, file, _, _ := runtime.Caller(0)
	for _, svc := range services {
		schema, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "services", svc, "schema.sql"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.Pool.Exec(ctx, string(schema)); err != nil {
			t.Fatalf("apply %s schema: %v", svc, err)
		}
	}
	return d
}
