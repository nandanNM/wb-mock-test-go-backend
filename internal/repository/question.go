package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Option is an MCQ choice. is_correct is included because the dashboard is
// admin-only; it must never be serialized on student-facing endpoints.
type Option struct {
	ID         int64  `json:"id"`
	QuestionID int64  `json:"question_id"`
	Position   int16  `json:"position"`
	BodyEN     string `json:"body_en"`
	BodyBN     string `json:"body_bn"`
	IsCorrect  bool   `json:"is_correct"`
}

type Question struct {
	ID            int64     `json:"id"`
	ChapterID     int64     `json:"chapter_id"`
	PromptEN      string    `json:"prompt_en"`
	PromptBN      string    `json:"prompt_bn"`
	ExplanationEN *string   `json:"explanation_en,omitempty"`
	ExplanationBN *string   `json:"explanation_bn,omitempty"`
	Position      int       `json:"position"`
	CreatedBy     *string   `json:"created_by,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	Options       []Option  `json:"options,omitempty"`
}

// OptionInput is a caller-supplied option for create/replace.
type OptionInput struct {
	Position  int16  `json:"position"`
	BodyEN    string `json:"body_en"`
	BodyBN    string `json:"body_bn"`
	IsCorrect bool   `json:"is_correct"`
}

type QuestionRepository struct{ pool *pgxpool.Pool }

func NewQuestionRepository(pool *pgxpool.Pool) *QuestionRepository { return &QuestionRepository{pool} }

const questionCols = `id, chapter_id, prompt_en, prompt_bn, explanation_en, explanation_bn, position, created_by::text, created_at, updated_at`

func scanQuestion(row pgx.Row, q *Question) error {
	return row.Scan(&q.ID, &q.ChapterID, &q.PromptEN, &q.PromptBN, &q.ExplanationEN, &q.ExplanationBN,
		&q.Position, &q.CreatedBy, &q.CreatedAt, &q.UpdatedAt)
}

// List returns questions (without options) for an optional chapter filter.
func (r *QuestionRepository) List(ctx context.Context, chapterID int64, p ListParams) ([]Question, int64, error) {
	conds, args := []string{}, []any{}
	if chapterID != 0 {
		args = append(args, chapterID)
		conds = append(conds, fmt.Sprintf("chapter_id = $%d", len(args)))
	}
	if p.Search != "" {
		args = append(args, "%"+p.Search+"%")
		conds = append(conds, fmt.Sprintf("(prompt_en ILIKE $%d OR prompt_bn ILIKE $%d)", len(args), len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM questions`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := fmt.Sprintf(`SELECT %s FROM questions%s %s LIMIT %d OFFSET %d`,
		questionCols, where, p.orderBy("position"), p.limit(), p.Offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Question, 0)
	for rows.Next() {
		var qq Question
		if err := scanQuestion(rows, &qq); err != nil {
			return nil, 0, err
		}
		out = append(out, qq)
	}
	return out, total, rows.Err()
}

// Get returns a question with its options.
func (r *QuestionRepository) Get(ctx context.Context, id int64) (Question, error) {
	var q Question
	err := scanQuestion(r.pool.QueryRow(ctx, `SELECT `+questionCols+` FROM questions WHERE id=$1`, id), &q)
	if errors.Is(err, pgx.ErrNoRows) {
		return Question{}, ErrNotFound
	}
	if err != nil {
		return Question{}, err
	}
	opts, err := r.options(ctx, r.pool, id)
	if err != nil {
		return Question{}, err
	}
	q.Options = opts
	return q, nil
}

func (r *QuestionRepository) options(ctx context.Context, q pgxQuerier, questionID int64) ([]Option, error) {
	rows, err := q.Query(ctx,
		`SELECT id, question_id, position, body_en, body_bn, is_correct
		 FROM question_options WHERE question_id=$1 ORDER BY position`, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Option, 0, 4)
	for rows.Next() {
		var o Option
		if err := rows.Scan(&o.ID, &o.QuestionID, &o.Position, &o.BodyEN, &o.BodyBN, &o.IsCorrect); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

type QuestionCreate struct {
	ChapterID     int64
	PromptEN      string
	PromptBN      string
	ExplanationEN *string
	ExplanationBN *string
	Position      int
	CreatedBy     string
	Options       []OptionInput
}

// Create inserts a question and its options in one transaction. The deferred
// validate_question_options trigger enforces >=2 options + exactly 1 correct at
// COMMIT; a violation surfaces as an error the handler maps to 422.
func (r *QuestionRepository) Create(ctx context.Context, c QuestionCreate) (Question, error) {
	var q Question
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if e := scanQuestion(tx.QueryRow(ctx,
			`INSERT INTO questions (chapter_id, prompt_en, prompt_bn, explanation_en, explanation_bn, position, created_by)
			 VALUES ($1,$2,$3,$4,$5,$6,NULLIF($7,'')::uuid) RETURNING `+questionCols,
			c.ChapterID, c.PromptEN, c.PromptBN, c.ExplanationEN, c.ExplanationBN, c.Position, c.CreatedBy), &q); e != nil {
			return e
		}
		return insertOptions(ctx, tx, q.ID, c.Options)
	})
	if err != nil {
		return Question{}, mapWrite(err)
	}
	q.Options, _ = r.options(ctx, r.pool, q.ID)
	return q, nil
}

type QuestionUpdate struct {
	PromptEN      *string
	PromptBN      *string
	ExplanationEN *string
	ExplanationBN *string
	Position      *int
	Options       []OptionInput // nil = leave options unchanged; non-nil = full replace
}

func (r *QuestionRepository) Update(ctx context.Context, id int64, u QuestionUpdate) (Question, error) {
	var q Question
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		e := scanQuestion(tx.QueryRow(ctx, `
			UPDATE questions SET
				prompt_en      = COALESCE($2, prompt_en),
				prompt_bn      = COALESCE($3, prompt_bn),
				explanation_en = COALESCE($4, explanation_en),
				explanation_bn = COALESCE($5, explanation_bn),
				position       = COALESCE($6, position)
			WHERE id=$1
			RETURNING `+questionCols, id, u.PromptEN, u.PromptBN, u.ExplanationEN, u.ExplanationBN, u.Position), &q)
		if errors.Is(e, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		if u.Options != nil { // full replace
			if _, e := tx.Exec(ctx, `DELETE FROM question_options WHERE question_id=$1`, id); e != nil {
				return e
			}
			return insertOptions(ctx, tx, id, u.Options)
		}
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return Question{}, ErrNotFound
	}
	if err != nil {
		return Question{}, mapWrite(err)
	}
	q.Options, _ = r.options(ctx, r.pool, id)
	return q, nil
}

func (r *QuestionRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM questions WHERE id=$1`, id)
	if err != nil {
		return mapWrite(err) // RESTRICT from test_questions/attempt_answers -> ErrInUse
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func insertOptions(ctx context.Context, tx pgx.Tx, questionID int64, opts []OptionInput) error {
	for _, o := range opts {
		if _, err := tx.Exec(ctx,
			`INSERT INTO question_options (question_id, position, body_en, body_bn, is_correct)
			 VALUES ($1,$2,$3,$4,$5)`, questionID, o.Position, o.BodyEN, o.BodyBN, o.IsCorrect); err != nil {
			return err
		}
	}
	return nil
}

// pgxQuerier is satisfied by both *pgxpool.Pool and pgx.Tx.
type pgxQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}
