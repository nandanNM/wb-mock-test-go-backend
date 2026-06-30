package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CatalogRepository serves the user-facing (client) browse flow:
// subjects -> chapters -> {published tests with per-user status, notes}.
// It is read-only and never exposes unpublished tests or authoring metadata.
type CatalogRepository struct{ pool *pgxpool.Pool }

func NewCatalogRepository(pool *pgxpool.Pool) *CatalogRepository { return &CatalogRepository{pool} }

// Subjects returns all subjects in display order.
func (r *CatalogRepository) Subjects(ctx context.Context) ([]Subject, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+subjectCols+` FROM subjects ORDER BY position, name_en`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Subject, 0)
	for rows.Next() {
		var s Subject
		if err := scanSubject(rows, &s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ChaptersBySubject returns a subject's chapters in display order.
func (r *CatalogRepository) ChaptersBySubject(ctx context.Context, subjectID int64) ([]Chapter, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+chapterCols+` FROM chapters WHERE subject_id=$1 ORDER BY position, name_en`, subjectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Chapter, 0)
	for rows.Next() {
		var c Chapter
		if err := scanChapter(rows, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// NotesByChapter returns a chapter's notes (study PDFs).
func (r *CatalogRepository) NotesByChapter(ctx context.Context, chapterID int64) ([]ChapterNote, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+noteCols+` FROM chapter_notes WHERE chapter_id=$1 ORDER BY language_code, title`, chapterID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ChapterNote, 0)
	for rows.Next() {
		var n ChapterNote
		if err := scanNote(rows, &n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// UserTest is a published test plus the calling user's attempt status.
type UserTest struct {
	ID              int64      `json:"id"`
	SubjectID       int64      `json:"subject_id"`
	ScopeType       string     `json:"scope_type"`
	TitleEN         string     `json:"title_en"`
	TitleBN         string     `json:"title_bn"`
	TestCode        string     `json:"test_code"`
	Difficulty      *string    `json:"difficulty,omitempty"`
	Position        int        `json:"position"`
	QuestionCount   int        `json:"question_count"`
	Status          string     `json:"status"` // not_attempted | in_progress | completed
	AttemptsCount   int        `json:"attempts_count"`
	BestScore       *int16     `json:"best_score,omitempty"`
	LastCompletedAt *time.Time `json:"last_completed_at,omitempty"`
}

// TestsByChapter returns the published tests that cover a chapter, each tagged
// with the given user's attempt status. One query, no N+1.
func (r *CatalogRepository) TestsByChapter(ctx context.Context, chapterID int64, userID string) ([]UserTest, error) {
	const q = `
		SELECT t.id, t.subject_id, t.scope_type, t.title_en, t.title_bn, t.test_code, t.difficulty, t.position,
		       (SELECT count(*) FROM test_questions tq WHERE tq.test_id = t.id) AS question_count,
		       COALESCE(a.completed_count, 0) AS completed_count,
		       COALESCE(a.in_progress_count, 0) AS in_progress_count,
		       a.best_score,
		       a.last_completed_at
		FROM tests t
		JOIN test_chapters tc ON tc.test_id = t.id
		LEFT JOIN (
			SELECT test_id,
			       count(*) FILTER (WHERE completed_at IS NOT NULL) AS completed_count,
			       count(*) FILTER (WHERE completed_at IS NULL)     AS in_progress_count,
			       max(score) FILTER (WHERE completed_at IS NOT NULL) AS best_score,
			       max(completed_at) AS last_completed_at
			FROM test_attempts WHERE user_id = $2 GROUP BY test_id
		) a ON a.test_id = t.id
		WHERE tc.chapter_id = $1 AND t.is_published = true
		ORDER BY t.position, t.title_en`

	rows, err := r.pool.Query(ctx, q, chapterID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]UserTest, 0)
	for rows.Next() {
		var (
			t          UserTest
			completed  int
			inProgress int
		)
		if err := rows.Scan(&t.ID, &t.SubjectID, &t.ScopeType, &t.TitleEN, &t.TitleBN, &t.TestCode,
			&t.Difficulty, &t.Position, &t.QuestionCount, &completed, &inProgress, &t.BestScore, &t.LastCompletedAt); err != nil {
			return nil, err
		}
		t.AttemptsCount = completed + inProgress
		switch {
		case completed > 0:
			t.Status = "completed"
		case inProgress > 0:
			t.Status = "in_progress"
		default:
			t.Status = "not_attempted"
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
