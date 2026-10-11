-- Payments' tables, in its own Postgres schema. The service connects with
-- search_path=payments.

create schema payments;

create table payments.charges (
  id              bigserial primary key,
  stripe_id       text not null,             -- the processor's charge id
  order_id        text not null,
  idempotency_key text not null,             -- deliberately not unique: payments dedups under an
  amount_cents    bigint not null,           -- advisory lock, so the race flag has something to break
  created_at      timestamptz not null default now()
);
create index charges_key on payments.charges (idempotency_key);
create index charges_order on payments.charges (order_id);
