package repository

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Attempt struct {
	ID              int64           `json:"id"`
	UserID          string          `json:"user_id"`
	TestID          int64           `json:"test_id"`
	BattleID        *int64          `json:"battle_id,omitempty"`
	LanguageCode    string          `json:"language_code"`
	Score           int16           `json:"score"`
	TotalQuestions  int16           `json:"total_questions"`
	Accuracy        float64         `json:"accuracy"`
	PointsEarned    int             `json:"points_earned"`
	DurationSeconds *int            `json:"duration_seconds,omitempty"`
	StartedAt       time.Time       `json:"started_at"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty"`
	Answers         []AttemptAnswer `json:"answers,omitempty"`
}

type AttemptAnswer struct {
	ID               int64     `json:"id"`
	QuestionID       int64     `json:"question_id"`
	SelectedOptionID *int64    `json:"selected_option_id,omitempty"`
	IsCorrect        bool      `json:"is_correct"`
	AnsweredAt       time.Time `json:"answered_at"`
}

type AttemptRepository struct{ pool *pgxpool.Pool }

func NewAttemptRepository(pool *pgxpool.Pool) *AttemptRepository { return &AttemptRepository{pool} }

const attemptCols = `id, user_id::text, test_id, battle_id, language_code, score, total_questions, accuracy::float8, points_earned, duration_seconds, started_at, completed_at`

func scanAttempt(row pgx.Row, a *Attempt) error {
	return row.Scan(&a.ID, &a.UserID, &a.TestID, &a.BattleID, &a.LanguageCode, &a.Score,
		&a.TotalQuestions, &a.Accuracy, &a.PointsEarned, &a.DurationSeconds, &a.StartedAt, &a.CompletedAt)
}

type AttemptFilter struct {
	UserID string
	TestID int64
}

func (r *AttemptRepository) List(ctx context.Context, f AttemptFilter, p ListParams) ([]Attempt, int64, error) {
	conds, args := []string{}, []any{}
	if f.UserID != "" {
		args = append(args, f.UserID)
		conds = append(conds, fmt.Sprintf("user_id = $%d", len(args)))
	}
	if f.TestID != 0 {
		args = append(args, f.TestID)
		conds = append(conds, fmt.Sprintf("test_id = $%d", len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + joinAnd(conds)
	}
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM test_attempts`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := fmt.Sprintf(`SELECT %s FROM test_attempts%s %s LIMIT %d OFFSET %d`,
		attemptCols, where, p.orderBy("started_at"), p.limit(), p.Offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Attempt, 0)
	for rows.Next() {
		var a Attempt
		if err := scanAttempt(rows, &a); err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

func (r *AttemptRepository) Get(ctx context.Context, id int64) (Attempt, error) {
	var a Attempt
	err := scanAttempt(r.pool.QueryRow(ctx, `SELECT `+attemptCols+` FROM test_attempts WHERE id=$1`, id), &a)
	if errors.Is(err, pgx.ErrNoRows) {
		return Attempt{}, ErrNotFound
	}
	if err != nil {
		return Attempt{}, err
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, question_id, selected_option_id, is_correct, answered_at
		 FROM attempt_answers WHERE attempt_id=$1 ORDER BY id`, id)
	if err != nil {
		return Attempt{}, err
	}
	defer rows.Close()
	a.Answers = make([]AttemptAnswer, 0)
	for rows.Next() {
		var ans AttemptAnswer
		if err := rows.Scan(&ans.ID, &ans.QuestionID, &ans.SelectedOptionID, &ans.IsCorrect, &ans.AnsweredAt); err != nil {
			return Attempt{}, err
		}
		a.Answers = append(a.Answers, ans)
	}
	return a, rows.Err()
}

func (r *AttemptRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM test_attempts WHERE id=$1`, id)
	if err != nil {
		return mapWrite(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func joinAnd(conds []string) string {
	out := ""
	for i, c := range conds {
		if i > 0 {
			out += " AND "
		}
		out += c
	}
	return out
}
