package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Follow struct {
	FollowerID string    `json:"follower_id"`
	FolloweeID string    `json:"followee_id"`
	CreatedAt  time.Time `json:"created_at"`
}

type FollowRepository struct{ pool *pgxpool.Pool }

func NewFollowRepository(pool *pgxpool.Pool) *FollowRepository { return &FollowRepository{pool} }

type FollowFilter struct {
	FollowerID string
	FolloweeID string
}

func (r *FollowRepository) List(ctx context.Context, f FollowFilter, p ListParams) ([]Follow, int64, error) {
	conds, args := []string{}, []any{}
	if f.FollowerID != "" {
		args = append(args, f.FollowerID)
		conds = append(conds, fmt.Sprintf("follower_id = $%d", len(args)))
	}
	if f.FolloweeID != "" {
		args = append(args, f.FolloweeID)
		conds = append(conds, fmt.Sprintf("followee_id = $%d", len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + joinAnd(conds)
	}
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM user_follows`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := fmt.Sprintf(`SELECT follower_id::text, followee_id::text, created_at FROM user_follows%s %s LIMIT %d OFFSET %d`,
		where, p.orderBy("created_at"), p.limit(), p.Offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Follow, 0)
	for rows.Next() {
		var f Follow
		if err := rows.Scan(&f.FollowerID, &f.FolloweeID, &f.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, f)
	}
	return out, total, rows.Err()
}

func (r *FollowRepository) Delete(ctx context.Context, followerID, followeeID string) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM user_follows WHERE follower_id=$1 AND followee_id=$2`, followerID, followeeID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
