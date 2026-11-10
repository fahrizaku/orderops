package user

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when no user matches a lookup.
var ErrNotFound = errors.New("user not found")

// Repository is the storage contract the service depends on. Handlers and
// business logic never touch SQL directly.
type Repository interface {
	Create(ctx context.Context, u *User) error
	ByEmail(ctx context.Context, email string) (*User, error)
	ByID(ctx context.Context, id int64) (*User, error)
}

// PostgresRepository stores users in the auth service's own database.
type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) Create(ctx context.Context, u *User) error {
	return r.pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, created_at`,
		u.Name, u.Email, u.PasswordHash, u.Role,
	).Scan(&u.ID, &u.CreatedAt)
}

func (r *PostgresRepository) ByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(r.pool.QueryRow(ctx,
		`SELECT id, name, email, password_hash, role, created_at
		   FROM users WHERE email = $1`, email))
}

func (r *PostgresRepository) ByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(r.pool.QueryRow(ctx,
		`SELECT id, name, email, password_hash, role, created_at
		   FROM users WHERE id = $1`, id))
}

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}
