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

type Test struct {
	ID          int64     `json:"id"`
	SubjectID   int64     `json:"subject_id"`
	ScopeType   string    `json:"scope_type"`
	TitleEN     string    `json:"title_en"`
	TitleBN     string    `json:"title_bn"`
	TestCode    string    `json:"test_code"`
	Difficulty  *string   `json:"difficulty,omitempty"`
	Position    int       `json:"position"`
	IsPublished bool      `json:"is_published"`
	CreatedBy   *string   `json:"created_by,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	ChapterIDs  []int64   `json:"chapter_ids,omitempty"`
	QuestionIDs []int64   `json:"question_ids,omitempty"`
}

type TestRepository struct{ pool *pgxpool.Pool }

func NewTestRepository(pool *pgxpool.Pool) *TestRepository { return &TestRepository{pool} }

const testCols = `id, subject_id, scope_type, title_en, title_bn, test_code, difficulty, position, is_published, created_by::text, created_at, updated_at`

func scanTest(row pgx.Row, t *Test) error {
	return row.Scan(&t.ID, &t.SubjectID, &t.ScopeType, &t.TitleEN, &t.TitleBN, &t.TestCode,
		&t.Difficulty, &t.Position, &t.IsPublished, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt)
}

// TestFilter narrows a list query.
type TestFilter struct {
	SubjectID int64
	Published *bool
}

func (r *TestRepository) List(ctx context.Context, f TestFilter, p ListParams) ([]Test, int64, error) {
	conds, args := []string{}, []any{}
	if f.SubjectID != 0 {
		args = append(args, f.SubjectID)
		conds = append(conds, fmt.Sprintf("subject_id = $%d", len(args)))
	}
	if f.Published != nil {
		args = append(args, *f.Published)
		conds = append(conds, fmt.Sprintf("is_published = $%d", len(args)))
	}
	if p.Search != "" {
		args = append(args, "%"+p.Search+"%")
		conds = append(conds, fmt.Sprintf("(title_en ILIKE $%d OR title_bn ILIKE $%d OR test_code ILIKE $%d)", len(args), len(args), len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM tests`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := fmt.Sprintf(`SELECT %s FROM tests%s %s LIMIT %d OFFSET %d`,
		testCols, where, p.orderBy("position"), p.limit(), p.Offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Test, 0)
	for rows.Next() {
		var t Test
		if err := scanTest(rows, &t); err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

func (r *TestRepository) Get(ctx context.Context, id int64) (Test, error) {
	var t Test
	err := scanTest(r.pool.QueryRow(ctx, `SELECT `+testCols+` FROM tests WHERE id=$1`, id), &t)
	if errors.Is(err, pgx.ErrNoRows) {
		return Test{}, ErrNotFound
	}
	if err != nil {
		return Test{}, err
	}
	if t.ChapterIDs, err = r.int64Col(ctx, `SELECT chapter_id FROM test_chapters WHERE test_id=$1 ORDER BY chapter_id`, id); err != nil {
		return Test{}, err
	}
	if t.QuestionIDs, err = r.int64Col(ctx, `SELECT question_id FROM test_questions WHERE test_id=$1 ORDER BY position`, id); err != nil {
		return Test{}, err
	}
	return t, nil
}

func (r *TestRepository) int64Col(ctx context.Context, sql string, args ...any) ([]int64, error) {
	rows, err := r.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]int64, 0)
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type TestCreate struct {
	SubjectID   int64
	ScopeType   string
	TitleEN     string
	TitleBN     string
	TestCode    string
	Difficulty  *string
	Position    int
	IsPublished bool
	CreatedBy   string
	ChapterIDs  []int64
	QuestionIDs []int64
}

func (r *TestRepository) Create(ctx context.Context, c TestCreate) (Test, error) {
	var t Test
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if e := scanTest(tx.QueryRow(ctx,
			`INSERT INTO tests (subject_id, scope_type, title_en, title_bn, test_code, difficulty, position, is_published, created_by)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,'')::uuid) RETURNING `+testCols,
			c.SubjectID, c.ScopeType, c.TitleEN, c.TitleBN, c.TestCode, c.Difficulty, c.Position, c.IsPublished, c.CreatedBy), &t); e != nil {
			return e
		}
		if e := setTestChapters(ctx, tx, t.ID, c.ChapterIDs); e != nil {
			return e
		}
		return setTestQuestions(ctx, tx, t.ID, c.QuestionIDs)
	})
	if err != nil {
		return Test{}, mapWrite(err)
	}
	return r.Get(ctx, t.ID)
}

type TestUpdate struct {
	TitleEN     *string
	TitleBN     *string
	ScopeType   *string
	Difficulty  *string
	Position    *int
	IsPublished *bool
	ChapterIDs  []int64 // nil = unchanged
	QuestionIDs []int64 // nil = unchanged
}

func (r *TestRepository) Update(ctx context.Context, id int64, u TestUpdate) (Test, error) {
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		tag, e := tx.Exec(ctx, `
			UPDATE tests SET
				title_en     = COALESCE($2, title_en),
				title_bn     = COALESCE($3, title_bn),
				scope_type   = COALESCE($4, scope_type),
				difficulty   = COALESCE($5, difficulty),
				position     = COALESCE($6, position),
				is_published = COALESCE($7, is_published)
			WHERE id=$1`,
			id, u.TitleEN, u.TitleBN, u.ScopeType, u.Difficulty, u.Position, u.IsPublished)
		if e != nil {
			return e
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		if u.ChapterIDs != nil {
			if _, e := tx.Exec(ctx, `DELETE FROM test_chapters WHERE test_id=$1`, id); e != nil {
				return e
			}
			if e := setTestChapters(ctx, tx, id, u.ChapterIDs); e != nil {
				return e
			}
		}
		if u.QuestionIDs != nil {
			if _, e := tx.Exec(ctx, `DELETE FROM test_questions WHERE test_id=$1`, id); e != nil {
				return e
			}
			if e := setTestQuestions(ctx, tx, id, u.QuestionIDs); e != nil {
				return e
			}
		}
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return Test{}, ErrNotFound
	}
	if err != nil {
		return Test{}, mapWrite(err)
	}
	return r.Get(ctx, id)
}

func (r *TestRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM tests WHERE id=$1`, id)
	if err != nil {
		return mapWrite(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func setTestChapters(ctx context.Context, tx pgx.Tx, testID int64, chapterIDs []int64) error {
	for _, cid := range chapterIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO test_chapters (test_id, chapter_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, testID, cid); err != nil {
			return err
		}
	}
	return nil
}

func setTestQuestions(ctx context.Context, tx pgx.Tx, testID int64, questionIDs []int64) error {
	for pos, qid := range questionIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO test_questions (test_id, question_id, position) VALUES ($1,$2,$3)`, testID, qid, pos); err != nil {
			return err
		}
	}
	return nil
}
