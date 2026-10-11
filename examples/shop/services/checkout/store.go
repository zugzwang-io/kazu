package checkout

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/zugzwang-io/kazu/examples/shop/internal/svc"
)

// PGStore is Store on Postgres. Every query runs on the caller's context, so
// deadlines flow from the request into the database.
type PGStore struct{ DB *svc.DB }

const orderCols = "id, idempotency_key, amount_cents, status"

func scanOrder(row pgx.Row) (Order, error) {
	var o Order
	err := row.Scan(&o.ID, &o.IdempotencyKey, &o.AmountCents, &o.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

// OrderID derives an order's id from its idempotency key, so ids are the same
// in every run that sends the same keys.
func OrderID(key string) string { return "ord_" + key }

func (s PGStore) CreateOrGet(ctx context.Context, key string, amountCents int64) (Order, error) {
	c, err := s.DB.Acquire(ctx)
	if err != nil {
		return Order{}, err
	}
	defer c.Release()
	o, err := scanOrder(c.QueryRow(ctx,
		`insert into orders (id, idempotency_key, amount_cents, status) values ($1, $2, $3, 'pending')
		 on conflict do nothing returning `+orderCols,
		OrderID(key), key, amountCents))
	if errors.Is(err, ErrNotFound) {
		return scanOrder(c.QueryRow(ctx, `select `+orderCols+` from orders where idempotency_key = $1`, key))
	}
	return o, err
}

func (s PGStore) Resolve(ctx context.Context, id, status string) (Order, error) {
	c, err := s.DB.Acquire(ctx)
	if err != nil {
		return Order{}, err
	}
	defer c.Release()
	o, err := scanOrder(c.QueryRow(ctx,
		`update orders set status = $2, updated_at = now() where id = $1 and status = 'pending' returning `+orderCols,
		id, status))
	if errors.Is(err, ErrNotFound) {
		return scanOrder(c.QueryRow(ctx, `select `+orderCols+` from orders where id = $1`, id))
	}
	return o, err
}

func (s PGStore) Get(ctx context.Context, id string) (Order, error) {
	c, err := s.DB.Acquire(ctx)
	if err != nil {
		return Order{}, err
	}
	defer c.Release()
	return scanOrder(c.QueryRow(ctx, `select `+orderCols+` from orders where id = $1`, id))
}

func (s PGStore) List(ctx context.Context, status string, limit int) ([]Order, error) {
	c, err := s.DB.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Release()
	return collect(c.Query(ctx,
		`select `+orderCols+` from orders where $1 = '' or status = $1 order by created_at desc limit $2`,
		status, limit))
}

func (s PGStore) ClaimStale(ctx context.Context, age time.Duration, limit int) ([]Order, error) {
	c, err := s.DB.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer c.Release()
	return collect(c.Query(ctx,
		`update orders set updated_at = now() where id in (
		   select id from orders
		   where status = 'pending' and updated_at < now() - make_interval(secs => $1)
		   order by updated_at limit $2 for update skip locked)
		 returning `+orderCols,
		age.Seconds(), limit))
}

func collect(rows pgx.Rows, err error) ([]Order, error) {
	if err != nil {
		return nil, err
	}
	orders := []Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		orders = append(orders, o)
	}
	return orders, rows.Err()
}
