// Package db is the shop's Postgres pool.
package db

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	URL              string
	PoolSize         int           // 10
	AcquireTimeout   time.Duration // 500ms
	StatementTimeout time.Duration // 2s
}

// DB is a pgx pool plus the acquire timeout every query waits at most:
// pgxpool has no acquire timeout of its own, and without one a drained pool
// makes requests wait for their whole deadline instead of failing fast.
type DB struct {
	Pool           *pgxpool.Pool
	AcquireTimeout time.Duration
}

// Open builds the pool. It connects lazily and redials after failures.
func Open(ctx context.Context, c Config) (*DB, error) {
	cfg, err := pgxpool.ParseConfig(c.URL)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = int32(c.PoolSize)
	cfg.HealthCheckPeriod = time.Second
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = strconv.Itoa(int(c.StatementTimeout.Milliseconds()))
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &DB{Pool: pool, AcquireTimeout: c.AcquireTimeout}, nil
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
