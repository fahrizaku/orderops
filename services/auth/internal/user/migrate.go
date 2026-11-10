package user

import (
	"context"
	_ "embed"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/0001_create_users.sql
var createUsersSQL string

// Migrate applies the auth service schema. The SQL is idempotent
// (CREATE TABLE IF NOT EXISTS), so it is safe to run on every start.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, createUsersSQL)
	return err
}
