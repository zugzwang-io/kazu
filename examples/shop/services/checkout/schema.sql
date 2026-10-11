-- Checkout's tables, in its own Postgres schema. The service connects with
-- search_path=checkout.

create schema checkout;

create table checkout.orders (
  id              text primary key,          -- 'ord_' + the idempotency key
  idempotency_key text not null unique,
  amount_cents    bigint not null,
  status          text not null check (status in ('pending', 'paid', 'failed')),
  created_at      timestamptz not null default now(),
  updated_at      timestamptz not null default now()
);
create index orders_pending on checkout.orders (updated_at) where status = 'pending';
