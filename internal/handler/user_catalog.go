package handler

import (
	"net/http"

	"backend/internal/httpx"
	"backend/internal/middleware"
)

// These are the user-facing (client/mobile) catalog endpoints. They require
// authentication (any logged-in user) and expose only published content.
// Browse flow: subjects -> chapters -> {tests with the user's status, notes}.

// catalogSubjects lists all subjects in display order.
func (a *API) catalogSubjects(w http.ResponseWriter, r *http.Request) error {
	subjects, err := a.catalog.Subjects(r.Context())
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	httpx.JSON(w, http.StatusOK, subjects)
	return nil
}

// catalogChapters lists the chapters of a subject.
func (a *API) catalogChapters(w http.ResponseWriter, r *http.Request) error {
	subjectID, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	chapters, err := a.catalog.ChaptersBySubject(r.Context(), subjectID)
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	httpx.JSON(w, http.StatusOK, chapters)
	return nil
}

// catalogTests lists a chapter's published tests, each tagged with the caller's
// attempt status (not_attempted | in_progress | completed).
func (a *API) catalogTests(w http.ResponseWriter, r *http.Request) error {
	chapterID, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	p, _ := middleware.PrincipalFromContext(r.Context())
	tests, err := a.catalog.TestsByChapter(r.Context(), chapterID, p.UserID)
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	httpx.JSON(w, http.StatusOK, tests)
	return nil
}

// catalogNotes lists a chapter's study notes.
func (a *API) catalogNotes(w http.ResponseWriter, r *http.Request) error {
	chapterID, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	notes, err := a.catalog.NotesByChapter(r.Context(), chapterID)
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	httpx.JSON(w, http.StatusOK, notes)
	return nil
}
