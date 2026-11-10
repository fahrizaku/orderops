CREATE TABLE IF NOT EXISTS orders (
    id           BIGSERIAL PRIMARY KEY,
    customer_id  BIGINT NOT NULL,
    store_name   TEXT NOT NULL,
    address      TEXT NOT NULL,
    subtotal     BIGINT NOT NULL,
    delivery_fee BIGINT NOT NULL DEFAULT 0,
    total        BIGINT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    courier_id   BIGINT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS order_items (
    id       BIGSERIAL PRIMARY KEY,
    order_id BIGINT NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
    name     TEXT NOT NULL,
    qty      INT NOT NULL,
    price    BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_order_items_order ON order_items (order_id);
CREATE INDEX IF NOT EXISTS idx_orders_customer ON orders (customer_id, created_at DESC);

-- Transactional outbox: order rows and their events are written in one
-- transaction; a relay publishes pending rows to RabbitMQ and stamps
-- sent_at, so an order can never exist without its event (or vice versa).
CREATE TABLE IF NOT EXISTS outbox_events (
    id           BIGSERIAL PRIMARY KEY,
    aggregate_id BIGINT NOT NULL,
    event_type   TEXT NOT NULL,
    payload      JSONB NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_outbox_pending ON outbox_events (id) WHERE sent_at IS NULL;
