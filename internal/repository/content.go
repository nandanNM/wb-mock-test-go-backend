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

// asPGCode reports whether err is a Postgres error with the given SQLSTATE code.
func asPGCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

// mapWrite turns common Postgres constraint violations into repository sentinels.
func mapWrite(err error) error {
	switch {
	case err == nil:
		return nil
	case asPGCode(err, "23505"): // unique_violation
		return ErrDuplicate
	case asPGCode(err, "23503"): // foreign_key_violation
		return ErrInUse
	default:
		return err
	}
}

// ---------------------------------------------------------------- Subjects ---

type Subject struct {
	ID        int64     `json:"id"`
	NameEN    string    `json:"name_en"`
	NameBN    string    `json:"name_bn"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type SubjectRepository struct{ pool *pgxpool.Pool }

func NewSubjectRepository(pool *pgxpool.Pool) *SubjectRepository { return &SubjectRepository{pool} }

const subjectCols = `id, name_en, name_bn, position, created_at, updated_at`

func scanSubject(row pgx.Row, s *Subject) error {
	return row.Scan(&s.ID, &s.NameEN, &s.NameBN, &s.Position, &s.CreatedAt, &s.UpdatedAt)
}

func (r *SubjectRepository) List(ctx context.Context, p ListParams) ([]Subject, int64, error) {
	var total int64
	where, args := "", []any{}
	if p.Search != "" {
		where = ` WHERE name_en ILIKE $1 OR name_bn ILIKE $1`
		args = append(args, "%"+p.Search+"%")
	}
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM subjects`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := fmt.Sprintf(`SELECT %s FROM subjects%s %s LIMIT %d OFFSET %d`,
		subjectCols, where, p.orderBy("position"), p.limit(), p.Offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Subject, 0)
	for rows.Next() {
		var s Subject
		if err := scanSubject(rows, &s); err != nil {
			return nil, 0, err
		}
		out = append(out, s)
	}
	return out, total, rows.Err()
}

func (r *SubjectRepository) Get(ctx context.Context, id int64) (Subject, error) {
	var s Subject
	err := scanSubject(r.pool.QueryRow(ctx, `SELECT `+subjectCols+` FROM subjects WHERE id=$1`, id), &s)
	if errors.Is(err, pgx.ErrNoRows) {
		return Subject{}, ErrNotFound
	}
	return s, err
}

func (r *SubjectRepository) Create(ctx context.Context, nameEN, nameBN string, position int) (Subject, error) {
	var s Subject
	err := scanSubject(r.pool.QueryRow(ctx,
		`INSERT INTO subjects (name_en, name_bn, position) VALUES ($1,$2,$3) RETURNING `+subjectCols,
		nameEN, nameBN, position), &s)
	return s, mapWrite(err)
}

// SubjectUpdate is a partial update; nil fields are left unchanged.
type SubjectUpdate struct {
	NameEN   *string
	NameBN   *string
	Position *int
}

func (r *SubjectRepository) Update(ctx context.Context, id int64, u SubjectUpdate) (Subject, error) {
	var s Subject
	err := scanSubject(r.pool.QueryRow(ctx, `
		UPDATE subjects SET
			name_en  = COALESCE($2, name_en),
			name_bn  = COALESCE($3, name_bn),
			position = COALESCE($4, position)
		WHERE id = $1
		RETURNING `+subjectCols, id, u.NameEN, u.NameBN, u.Position), &s)
	if errors.Is(err, pgx.ErrNoRows) {
		return Subject{}, ErrNotFound
	}
	return s, mapWrite(err)
}

func (r *SubjectRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM subjects WHERE id=$1`, id)
	if err != nil {
		return mapWrite(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------- Chapters ---

type Chapter struct {
	ID        int64     `json:"id"`
	SubjectID int64     `json:"subject_id"`
	NameEN    string    `json:"name_en"`
	NameBN    string    `json:"name_bn"`
	Position  int       `json:"position"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ChapterRepository struct{ pool *pgxpool.Pool }

func NewChapterRepository(pool *pgxpool.Pool) *ChapterRepository { return &ChapterRepository{pool} }

const chapterCols = `id, subject_id, name_en, name_bn, position, created_at, updated_at`

func scanChapter(row pgx.Row, c *Chapter) error {
	return row.Scan(&c.ID, &c.SubjectID, &c.NameEN, &c.NameBN, &c.Position, &c.CreatedAt, &c.UpdatedAt)
}

// List supports an optional subject_id filter (0 = all).
func (r *ChapterRepository) List(ctx context.Context, subjectID int64, p ListParams) ([]Chapter, int64, error) {
	conds, args := []string{}, []any{}
	if subjectID != 0 {
		args = append(args, subjectID)
		conds = append(conds, fmt.Sprintf("subject_id = $%d", len(args)))
	}
	if p.Search != "" {
		args = append(args, "%"+p.Search+"%")
		conds = append(conds, fmt.Sprintf("(name_en ILIKE $%d OR name_bn ILIKE $%d)", len(args), len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM chapters`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := fmt.Sprintf(`SELECT %s FROM chapters%s %s LIMIT %d OFFSET %d`,
		chapterCols, where, p.orderBy("position"), p.limit(), p.Offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Chapter, 0)
	for rows.Next() {
		var c Chapter
		if err := scanChapter(rows, &c); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

func (r *ChapterRepository) Get(ctx context.Context, id int64) (Chapter, error) {
	var c Chapter
	err := scanChapter(r.pool.QueryRow(ctx, `SELECT `+chapterCols+` FROM chapters WHERE id=$1`, id), &c)
	if errors.Is(err, pgx.ErrNoRows) {
		return Chapter{}, ErrNotFound
	}
	return c, err
}

func (r *ChapterRepository) Create(ctx context.Context, subjectID int64, nameEN, nameBN string, position int) (Chapter, error) {
	var c Chapter
	err := scanChapter(r.pool.QueryRow(ctx,
		`INSERT INTO chapters (subject_id, name_en, name_bn, position) VALUES ($1,$2,$3,$4) RETURNING `+chapterCols,
		subjectID, nameEN, nameBN, position), &c)
	return c, mapWrite(err)
}

type ChapterUpdate struct {
	NameEN   *string
	NameBN   *string
	Position *int
}

func (r *ChapterRepository) Update(ctx context.Context, id int64, u ChapterUpdate) (Chapter, error) {
	var c Chapter
	err := scanChapter(r.pool.QueryRow(ctx, `
		UPDATE chapters SET
			name_en  = COALESCE($2, name_en),
			name_bn  = COALESCE($3, name_bn),
			position = COALESCE($4, position)
		WHERE id = $1
		RETURNING `+chapterCols, id, u.NameEN, u.NameBN, u.Position), &c)
	if errors.Is(err, pgx.ErrNoRows) {
		return Chapter{}, ErrNotFound
	}
	return c, mapWrite(err)
}

func (r *ChapterRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM chapters WHERE id=$1`, id)
	if err != nil {
		return mapWrite(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ------------------------------------------------------------ ChapterNotes ---

type ChapterNote struct {
	ID           int64     `json:"id"`
	ChapterID    int64     `json:"chapter_id"`
	LanguageCode string    `json:"language_code"`
	Title        string    `json:"title"`
	PDFURL       string    `json:"pdf_url"`
	PageCount    *int      `json:"page_count,omitempty"`
	CreatedBy    *string   `json:"created_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ChapterNoteRepository struct{ pool *pgxpool.Pool }

func NewChapterNoteRepository(pool *pgxpool.Pool) *ChapterNoteRepository {
	return &ChapterNoteRepository{pool}
}

const noteCols = `id, chapter_id, language_code, title, pdf_url, page_count, created_by::text, created_at, updated_at`

func scanNote(row pgx.Row, n *ChapterNote) error {
	return row.Scan(&n.ID, &n.ChapterID, &n.LanguageCode, &n.Title, &n.PDFURL, &n.PageCount, &n.CreatedBy, &n.CreatedAt, &n.UpdatedAt)
}

func (r *ChapterNoteRepository) List(ctx context.Context, chapterID int64, p ListParams) ([]ChapterNote, int64, error) {
	where, args := "", []any{}
	if chapterID != 0 {
		where = " WHERE chapter_id = $1"
		args = append(args, chapterID)
	}
	var total int64
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM chapter_notes`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := fmt.Sprintf(`SELECT %s FROM chapter_notes%s %s LIMIT %d OFFSET %d`,
		noteCols, where, p.orderBy("created_at"), p.limit(), p.Offset)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]ChapterNote, 0)
	for rows.Next() {
		var n ChapterNote
		if err := scanNote(rows, &n); err != nil {
			return nil, 0, err
		}
		out = append(out, n)
	}
	return out, total, rows.Err()
}

func (r *ChapterNoteRepository) Get(ctx context.Context, id int64) (ChapterNote, error) {
	var n ChapterNote
	err := scanNote(r.pool.QueryRow(ctx, `SELECT `+noteCols+` FROM chapter_notes WHERE id=$1`, id), &n)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChapterNote{}, ErrNotFound
	}
	return n, err
}

type NoteCreate struct {
	ChapterID    int64
	LanguageCode string
	Title        string
	PDFURL       string
	PageCount    *int
	CreatedBy    string
}

func (r *ChapterNoteRepository) Create(ctx context.Context, c NoteCreate) (ChapterNote, error) {
	var n ChapterNote
	err := scanNote(r.pool.QueryRow(ctx,
		`INSERT INTO chapter_notes (chapter_id, language_code, title, pdf_url, page_count, created_by)
		 VALUES ($1,$2,$3,$4,$5,NULLIF($6,'')::uuid) RETURNING `+noteCols,
		c.ChapterID, c.LanguageCode, c.Title, c.PDFURL, c.PageCount, c.CreatedBy), &n)
	return n, mapWrite(err)
}

type NoteUpdate struct {
	Title     *string
	PDFURL    *string
	PageCount *int
}

func (r *ChapterNoteRepository) Update(ctx context.Context, id int64, u NoteUpdate) (ChapterNote, error) {
	var n ChapterNote
	err := scanNote(r.pool.QueryRow(ctx, `
		UPDATE chapter_notes SET
			title      = COALESCE($2, title),
			pdf_url    = COALESCE($3, pdf_url),
			page_count = COALESCE($4, page_count)
		WHERE id = $1
		RETURNING `+noteCols, id, u.Title, u.PDFURL, u.PageCount), &n)
	if errors.Is(err, pgx.ErrNoRows) {
		return ChapterNote{}, ErrNotFound
	}
	return n, mapWrite(err)
}

func (r *ChapterNoteRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM chapter_notes WHERE id=$1`, id)
	if err != nil {
		return mapWrite(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
