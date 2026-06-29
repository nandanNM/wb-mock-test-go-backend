package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Battle struct {
	ID           int64               `json:"id"`
	RoomCode     string              `json:"room_code"`
	HostID       string              `json:"host_id"`
	TestID       int64               `json:"test_id"`
	Status       string              `json:"status"`
	MaxPlayers   int16               `json:"max_players"`
	StartedAt    *time.Time          `json:"started_at,omitempty"`
	FinishedAt   *time.Time          `json:"finished_at,omitempty"`
	CreatedAt    time.Time           `json:"created_at"`
	UpdatedAt    time.Time           `json:"updated_at"`
	Participants []BattleParticipant `json:"participants,omitempty"`
}

type BattleParticipant struct {
	UserID     string     `json:"user_id"`
	Role       string     `json:"role"`
	Score      *int16     `json:"score,omitempty"`
	Placement  *int16     `json:"placement,omitempty"`
	JoinedAt   time.Time  `json:"joined_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type BattleRepository struct{ pool *pgxpool.Pool }

func NewBattleRepository(pool *pgxpool.Pool) *BattleRepository { return &BattleRepository{pool} }

const battleCols = `id, room_code, host_id::text, test_id, status, max_players, started_at, finished_at, created_at, updated_at`

func scanBattle(row pgx.Row, b *Battle) error {
	return row.Scan(&b.ID, &b.RoomCode, &b.HostID, &b.TestID, &b.Status, &b.MaxPlayers,
		&b.StartedAt, &b.FinishedAt, &b.CreatedAt, &b.UpdatedAt)
}

func (r *BattleRepository) List(ctx context.Context, status string, p ListParams) ([]Battle, int64, error) {
	where, args := "", []any{}
	if status != "" {
		where = " WHERE status = $1"
		args = append(args, status)
	}
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM battles`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := fmt.Sprintf(`SELECT %s FROM battles%s %s LIMIT %d OFFSET %d`,
		battleCols, where, p.orderBy("created_at"), p.limit(), p.Offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Battle, 0)
	for rows.Next() {
		var b Battle
		if err := scanBattle(rows, &b); err != nil {
			return nil, 0, err
		}
		out = append(out, b)
	}
	return out, total, rows.Err()
}

func (r *BattleRepository) Get(ctx context.Context, id int64) (Battle, error) {
	var b Battle
	err := scanBattle(r.pool.QueryRow(ctx, `SELECT `+battleCols+` FROM battles WHERE id=$1`, id), &b)
	if errors.Is(err, pgx.ErrNoRows) {
		return Battle{}, ErrNotFound
	}
	if err != nil {
		return Battle{}, err
	}
	rows, err := r.pool.Query(ctx,
		`SELECT user_id::text, role, score, placement, joined_at, finished_at
		 FROM battle_participants WHERE battle_id=$1 ORDER BY placement NULLS LAST, joined_at`, id)
	if err != nil {
		return Battle{}, err
	}
	defer rows.Close()
	b.Participants = make([]BattleParticipant, 0)
	for rows.Next() {
		var pt BattleParticipant
		if err := rows.Scan(&pt.UserID, &pt.Role, &pt.Score, &pt.Placement, &pt.JoinedAt, &pt.FinishedAt); err != nil {
			return Battle{}, err
		}
		b.Participants = append(b.Participants, pt)
	}
	return b, rows.Err()
}

// ForceFinish marks a battle finished (admin moderation). Returns ErrNotFound if
// the battle doesn't exist or is already finished/abandoned.
func (r *BattleRepository) ForceFinish(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE battles SET status='finished', finished_at=now()
		 WHERE id=$1 AND status IN ('lobby','active')`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *BattleRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM battles WHERE id=$1`, id)
	if err != nil {
		return mapWrite(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
