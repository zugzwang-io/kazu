package payments

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/zugzwang-io/kazu/examples/shop/internal/svc"
)

// PGStore is Store on Postgres.
type PGStore struct{ DB *svc.DB }

const chargeCols = "stripe_id, order_id, idempotency_key, amount_cents"

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func find(ctx context.Context, q querier, key string) (Charge, error) {
	var c Charge
	err := q.QueryRow(ctx, `select `+chargeCols+` from charges where idempotency_key = $1 order by id limit 1`, key).
		Scan(&c.ID, &c.OrderID, &c.IdempotencyKey, &c.AmountCents)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

func (s PGStore) Find(ctx context.Context, key string) (Charge, error) {
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		return Charge{}, err
	}
	defer conn.Release()
	return find(ctx, conn, key)
}

func (s PGStore) InsertOnce(ctx context.Context, c Charge) (Charge, error) {
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		return Charge{}, err
	}
	defer conn.Release()
	tx, err := conn.Begin(ctx)
	if err != nil {
		return Charge{}, err
	}
	defer tx.Rollback(ctx)

	// Lock, look up, insert if absent. The lock is released at commit.
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock(hashtext($1))`, c.IdempotencyKey); err != nil {
		return Charge{}, err
	}
	existing, err := find(ctx, tx, c.IdempotencyKey)
	if err == nil {
		return existing, tx.Commit(ctx)
	}
	if !errors.Is(err, ErrNotFound) {
		return Charge{}, err
	}
	if _, err := tx.Exec(ctx,
		`insert into charges (stripe_id, order_id, idempotency_key, amount_cents) values ($1, $2, $3, $4)`,
		c.ID, c.OrderID, c.IdempotencyKey, c.AmountCents); err != nil {
		return Charge{}, err
	}
	return c, tx.Commit(ctx)
}
