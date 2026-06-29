// Package repository is the data-access layer. It owns all SQL and maps rows to
// domain models, keeping query details out of the handlers and services.
package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("record not found")

// ErrDuplicate is returned on a unique-constraint violation.
var ErrDuplicate = errors.New("record already exists")

// User is the domain model for a user row.
type User struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Email         string    `json:"email"`
	Status        string    `json:"status"` // active | suspended | banned
	EmailVerified bool      `json:"email_verified"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// userColumns is the canonical projection used by all user SELECTs.
const userColumns = `id, name, email, status, email_verified, created_at, updated_at`

func scanUser(row pgx.Row, u *User) error {
	return row.Scan(&u.ID, &u.Name, &u.Email, &u.Status, &u.EmailVerified, &u.CreatedAt, &u.UpdatedAt)
}

// UserRepository provides access to the users table.
type UserRepository struct {
	pool *pgxpool.Pool
}

// NewUserRepository constructs a UserRepository backed by the given pool.
func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

// Create inserts a new user and returns the persisted row (with generated id
// and timestamps). Returns ErrDuplicate if the email already exists.
func (r *UserRepository) Create(ctx context.Context, name, email string) (User, error) {
	const q = `
		INSERT INTO users (name, email)
		VALUES ($1, $2)
		RETURNING ` + userColumns

	var u User
	err := scanUser(r.pool.QueryRow(ctx, q, name, email), &u)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return User{}, ErrDuplicate
		}
		return User{}, err
	}
	return u, nil
}

// GetByID fetches a user by id. Returns ErrNotFound if absent.
func (r *UserRepository) GetByID(ctx context.Context, id string) (User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE id = $1`

	var u User
	err := scanUser(r.pool.QueryRow(ctx, q, id), &u)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	return u, nil
}

// UserFilter narrows a dashboard user list.
type UserFilter struct {
	Status string // active | suspended | banned
	Search string // matches name or email
}

// ListPage returns a paginated, filtered page of users plus the total count.
func (r *UserRepository) ListPage(ctx context.Context, f UserFilter, p ListParams) ([]User, int64, error) {
	conds, args := []string{}, []any{}
	if f.Status != "" {
		args = append(args, f.Status)
		conds = append(conds, fmt.Sprintf("status = $%d", len(args)))
	}
	if f.Search != "" {
		args = append(args, "%"+f.Search+"%")
		conds = append(conds, fmt.Sprintf("(name ILIKE $%d OR email ILIKE $%d)", len(args), len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM users`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	sort := p.orderBy("created_at")
	q := `SELECT ` + userColumns + ` FROM users` + where + ` ` + sort +
		fmt.Sprintf(` LIMIT %d OFFSET %d`, p.limit(), p.Offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]User, 0)
	for rows.Next() {
		var u User
		if err := scanUser(rows, &u); err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}

// Delete hard-deletes a user (super-admin only; cascades to owned rows).
func (r *UserRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetStatus updates a user's account status and reason (ban/suspend/reinstate).
// Returns ErrNotFound if no such user.
func (r *UserRepository) SetStatus(ctx context.Context, id, status, reason string) error {
	const q = `UPDATE users SET status = $2, status_reason = $3 WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, id, status, reason)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// List returns users ordered by newest first, capped by limit.
func (r *UserRepository) List(ctx context.Context, limit int) ([]User, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	const q = `SELECT ` + userColumns + ` FROM users ORDER BY created_at DESC LIMIT $1`

	rows, err := r.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	users := make([]User, 0)
	for rows.Next() {
		var u User
		if err := scanUser(rows, &u); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}
