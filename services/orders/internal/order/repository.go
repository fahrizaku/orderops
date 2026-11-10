package order

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no order matches a lookup.
var ErrNotFound = errors.New("order not found")

// Repository is the storage contract of the orders service, including the
// transactional outbox used to publish events reliably.
type Repository interface {
	// CreateWithOutbox stores the order, its items and its order.created
	// outbox event in a single transaction.
	CreateWithOutbox(ctx context.Context, o *Order) error
	ByID(ctx context.Context, id int64) (*Order, error)
	ListByCustomer(ctx context.Context, customerID int64, limit, offset int) ([]*Order, error)
	ListAll(ctx context.Context, limit, offset int) ([]*Order, error)
	UpdateStatus(ctx context.Context, id int64, status Status, courierID *int64) error
	PendingOutbox(ctx context.Context, limit int) ([]OutboxEvent, error)
	MarkOutboxSent(ctx context.Context, id int64) error
}

// PostgresRepository stores orders in the orders service's own database.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) CreateWithOutbox(ctx context.Context, o *Order) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	err = tx.QueryRow(ctx,
		`INSERT INTO orders (customer_id, store_name, address, subtotal, delivery_fee, total, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, created_at, updated_at`,
		o.CustomerID, o.StoreName, o.Address, o.Subtotal, o.DeliveryFee, o.Total, o.Status,
	).Scan(&o.ID, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return err
	}

	for _, it := range o.Items {
		if _, err := tx.Exec(ctx,
			`INSERT INTO order_items (order_id, name, qty, price) VALUES ($1, $2, $3, $4)`,
			o.ID, it.Name, it.Qty, it.Price); err != nil {
			return err
		}
	}

	payload, err := json.Marshal(OrderCreatedEvent{
		OrderID:    o.ID,
		CustomerID: o.CustomerID,
		StoreName:  o.StoreName,
		Total:      o.Total,
		Status:     o.Status,
		OccurredAt: o.CreatedAt,
	})
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO outbox_events (aggregate_id, event_type, payload) VALUES ($1, 'order.created', $2)`,
		o.ID, payload); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (r *PostgresRepository) ByID(ctx context.Context, id int64) (*Order, error) {
	o, err := scanOrder(r.pool.QueryRow(ctx,
		`SELECT id, customer_id, store_name, address, subtotal, delivery_fee, total,
		        status, courier_id, created_at, updated_at
		   FROM orders WHERE id = $1`, id))
	if err != nil {
		return nil, err
	}
	if err := r.loadItems(ctx, r.pool, o); err != nil {
		return nil, err
	}
	return o, nil
}

func (r *PostgresRepository) ListByCustomer(ctx context.Context, customerID int64, limit, offset int) ([]*Order, error) {
	return r.list(ctx,
		`SELECT id, customer_id, store_name, address, subtotal, delivery_fee, total,
		        status, courier_id, created_at, updated_at
		   FROM orders WHERE customer_id = $1
		  ORDER BY created_at DESC LIMIT $2 OFFSET $3`, customerID, limit, offset)
}

func (r *PostgresRepository) ListAll(ctx context.Context, limit, offset int) ([]*Order, error) {
	return r.list(ctx,
		`SELECT id, customer_id, store_name, address, subtotal, delivery_fee, total,
		        status, courier_id, created_at, updated_at
		   FROM orders
		  ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
}

func (r *PostgresRepository) list(ctx context.Context, query string, args ...any) ([]*Order, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var orders []*Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		orders = append(orders, o)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, o := range orders {
		if err := r.loadItems(ctx, r.pool, o); err != nil {
			return nil, err
		}
	}
	return orders, nil
}

func (r *PostgresRepository) UpdateStatus(ctx context.Context, id int64, status Status, courierID *int64) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE orders
		    SET status = $2, courier_id = COALESCE($3, courier_id), updated_at = now()
		  WHERE id = $1`, id, status, courierID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *PostgresRepository) PendingOutbox(ctx context.Context, limit int) ([]OutboxEvent, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, payload FROM outbox_events
		  WHERE sent_at IS NULL ORDER BY id ASC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []OutboxEvent
	for rows.Next() {
		var (
			id      int64
			payload []byte
		)
		if err := rows.Scan(&id, &payload); err != nil {
			return nil, err
		}
		var ev OrderCreatedEvent
		if err := json.Unmarshal(payload, &ev); err != nil {
			return nil, err
		}
		ev.EventID = id
		events = append(events, OutboxEvent{ID: id, Payload: ev})
	}
	return events, rows.Err()
}

func (r *PostgresRepository) MarkOutboxSent(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE outbox_events SET sent_at = now() WHERE id = $1`, id)
	return err
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOrder(row rowScanner) (*Order, error) {
	var o Order
	err := row.Scan(&o.ID, &o.CustomerID, &o.StoreName, &o.Address, &o.Subtotal,
		&o.DeliveryFee, &o.Total, &o.Status, &o.CourierID, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func (r *PostgresRepository) loadItems(ctx context.Context, q querier, o *Order) error {
	rows, err := q.Query(ctx,
		`SELECT name, qty, price FROM order_items WHERE order_id = $1 ORDER BY id`, o.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	o.Items = []Item{}
	for rows.Next() {
		var it Item
		if err := rows.Scan(&it.Name, &it.Qty, &it.Price); err != nil {
			return err
		}
		o.Items = append(o.Items, it)
	}
	return rows.Err()
}
