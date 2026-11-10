package order

import (
	"context"
	_ "embed"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/0001_create_orders.sql
var createOrdersSQL string

// Migrate applies the orders service schema. The SQL is idempotent, so it
// is safe to run on every start.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, createOrdersSQL)
	return err
}
