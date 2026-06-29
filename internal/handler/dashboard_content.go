package handler

import (
	"errors"
	"net/http"
	"strings"

	"backend/internal/httpx"
	"backend/internal/repository"
)

// writeRepoError maps repository sentinels to client-safe API errors.
func writeRepoError(err error, notFoundField string, id any) error {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		return httpx.ErrNotFound.WithDetails(map[string]any{notFoundField: id})
	case errors.Is(err, repository.ErrDuplicate):
		return httpx.ErrConflict.WithDetails(map[string]any{"reason": "a record with these values already exists"})
	case errors.Is(err, repository.ErrInUse):
		return httpx.ErrConflict.WithDetails(map[string]any{"reason": "record is referenced by other records and cannot be deleted"})
	default:
		return httpx.Wrap(httpx.ErrInternal, err)
	}
}

// ------------------------------------------------------------- Subjects -----

var subjectSort = map[string]string{"position": "position", "name": "name_en", "created_at": "created_at"}

func (a *API) listSubjects(w http.ResponseWriter, r *http.Request) error {
	q := parseListQuery(r, subjectSort, "position")
	items, total, err := a.subjects.List(r.Context(), q.params())
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	writeList(w, q, total, items)
	return nil
}

func (a *API) getSubject(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	s, err := a.subjects.Get(r.Context(), id)
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	httpx.JSON(w, http.StatusOK, s)
	return nil
}

type subjectCreateReq struct {
	NameEN   string `json:"name_en"`
	NameBN   string `json:"name_bn"`
	Position int    `json:"position"`
}

func (a *API) createSubject(w http.ResponseWriter, r *http.Request) error {
	var req subjectCreateReq
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if fields := requireFields(map[string]string{"name_en": req.NameEN, "name_bn": req.NameBN}); fields != nil {
		return httpx.ErrValidation.WithDetails(fields)
	}
	s, err := a.subjects.Create(r.Context(), strings.TrimSpace(req.NameEN), strings.TrimSpace(req.NameBN), req.Position)
	if err != nil {
		return writeRepoError(err, "id", nil)
	}
	a.auditDash(r, "dashboard.subject.created", map[string]any{"subject_id": s.ID})
	httpx.JSON(w, http.StatusCreated, s)
	return nil
}

type subjectUpdateReq struct {
	NameEN   *string `json:"name_en"`
	NameBN   *string `json:"name_bn"`
	Position *int    `json:"position"`
}

func (a *API) updateSubject(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	var req subjectUpdateReq
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	s, err := a.subjects.Update(r.Context(), id, repository.SubjectUpdate{NameEN: req.NameEN, NameBN: req.NameBN, Position: req.Position})
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	a.auditDash(r, "dashboard.subject.updated", map[string]any{"subject_id": id})
	httpx.JSON(w, http.StatusOK, s)
	return nil
}

func (a *API) deleteSubject(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	if err := a.subjects.Delete(r.Context(), id); err != nil {
		return writeRepoError(err, "id", id)
	}
	a.auditDash(r, "dashboard.subject.deleted", map[string]any{"subject_id": id})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
	return nil
}

// ------------------------------------------------------------- Chapters -----

var chapterSort = map[string]string{"position": "position", "name": "name_en", "created_at": "created_at"}

func (a *API) listChapters(w http.ResponseWriter, r *http.Request) error {
	q := parseListQuery(r, chapterSort, "position")
	items, total, err := a.chapters.List(r.Context(), queryInt64(r, "subject_id"), q.params())
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	writeList(w, q, total, items)
	return nil
}

func (a *API) getChapter(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	c, err := a.chapters.Get(r.Context(), id)
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	httpx.JSON(w, http.StatusOK, c)
	return nil
}

type chapterCreateReq struct {
	SubjectID int64  `json:"subject_id"`
	NameEN    string `json:"name_en"`
	NameBN    string `json:"name_bn"`
	Position  int    `json:"position"`
}

func (a *API) createChapter(w http.ResponseWriter, r *http.Request) error {
	var req chapterCreateReq
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	fields := requireFields(map[string]string{"name_en": req.NameEN, "name_bn": req.NameBN})
	if req.SubjectID == 0 {
		if fields == nil {
			fields = map[string]any{}
		}
		fields["subject_id"] = "is required"
	}
	if fields != nil {
		return httpx.ErrValidation.WithDetails(fields)
	}
	c, err := a.chapters.Create(r.Context(), req.SubjectID, strings.TrimSpace(req.NameEN), strings.TrimSpace(req.NameBN), req.Position)
	if err != nil {
		return writeRepoError(err, "subject_id", req.SubjectID)
	}
	a.auditDash(r, "dashboard.chapter.created", map[string]any{"chapter_id": c.ID})
	httpx.JSON(w, http.StatusCreated, c)
	return nil
}

type chapterUpdateReq struct {
	NameEN   *string `json:"name_en"`
	NameBN   *string `json:"name_bn"`
	Position *int    `json:"position"`
}

func (a *API) updateChapter(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	var req chapterUpdateReq
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	c, err := a.chapters.Update(r.Context(), id, repository.ChapterUpdate{NameEN: req.NameEN, NameBN: req.NameBN, Position: req.Position})
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	a.auditDash(r, "dashboard.chapter.updated", map[string]any{"chapter_id": id})
	httpx.JSON(w, http.StatusOK, c)
	return nil
}

func (a *API) deleteChapter(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	if err := a.chapters.Delete(r.Context(), id); err != nil {
		return writeRepoError(err, "id", id)
	}
	a.auditDash(r, "dashboard.chapter.deleted", map[string]any{"chapter_id": id})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
	return nil
}

// --------------------------------------------------------- Chapter notes -----

var noteSort = map[string]string{"created_at": "created_at", "title": "title"}

func (a *API) listNotes(w http.ResponseWriter, r *http.Request) error {
	q := parseListQuery(r, noteSort, "created_at")
	items, total, err := a.notes.List(r.Context(), queryInt64(r, "chapter_id"), q.params())
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	writeList(w, q, total, items)
	return nil
}

func (a *API) getNote(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	n, err := a.notes.Get(r.Context(), id)
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	httpx.JSON(w, http.StatusOK, n)
	return nil
}

type noteCreateReq struct {
	ChapterID    int64  `json:"chapter_id"`
	LanguageCode string `json:"language_code"`
	Title        string `json:"title"`
	PDFURL       string `json:"pdf_url"`
	PageCount    *int   `json:"page_count"`
}

func (a *API) createNote(w http.ResponseWriter, r *http.Request) error {
	var req noteCreateReq
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	fields := requireFields(map[string]string{"title": req.Title, "pdf_url": req.PDFURL})
	if req.ChapterID == 0 {
		fields = ensure(fields)
		fields["chapter_id"] = "is required"
	}
	if req.LanguageCode != "en" && req.LanguageCode != "bn" {
		fields = ensure(fields)
		fields["language_code"] = "must be 'en' or 'bn'"
	}
	if fields != nil {
		return httpx.ErrValidation.WithDetails(fields)
	}
	actor, _ := principalUserID(r)
	n, err := a.notes.Create(r.Context(), repository.NoteCreate{
		ChapterID: req.ChapterID, LanguageCode: req.LanguageCode,
		Title: strings.TrimSpace(req.Title), PDFURL: strings.TrimSpace(req.PDFURL),
		PageCount: req.PageCount, CreatedBy: actor,
	})
	if err != nil {
		return writeRepoError(err, "chapter_id", req.ChapterID)
	}
	a.auditDash(r, "dashboard.note.created", map[string]any{"note_id": n.ID})
	httpx.JSON(w, http.StatusCreated, n)
	return nil
}

type noteUpdateReq struct {
	Title     *string `json:"title"`
	PDFURL    *string `json:"pdf_url"`
	PageCount *int    `json:"page_count"`
}

func (a *API) updateNote(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	var req noteUpdateReq
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	n, err := a.notes.Update(r.Context(), id, repository.NoteUpdate{Title: req.Title, PDFURL: req.PDFURL, PageCount: req.PageCount})
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	a.auditDash(r, "dashboard.note.updated", map[string]any{"note_id": id})
	httpx.JSON(w, http.StatusOK, n)
	return nil
}

func (a *API) deleteNote(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	if err := a.notes.Delete(r.Context(), id); err != nil {
		return writeRepoError(err, "id", id)
	}
	a.auditDash(r, "dashboard.note.deleted", map[string]any{"note_id": id})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
	return nil
}

// --------------------------------------------------------------- helpers -----

// requireFields returns a validation-details map for any blank values, or nil.
func requireFields(vals map[string]string) map[string]any {
	var out map[string]any
	for k, v := range vals {
		if strings.TrimSpace(v) == "" {
			out = ensure(out)
			out[k] = "is required"
		}
	}
	return out
}

func ensure(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
