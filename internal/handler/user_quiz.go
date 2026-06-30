package handler

import (
	"errors"
	"net/http"
	"time"

	"backend/internal/httpx"
	"backend/internal/middleware"
	"backend/internal/repository"
)

func validLang(code string) (string, bool) {
	switch code {
	case "", "en":
		return "en", true
	case "bn":
		return "bn", true
	default:
		return "", false
	}
}

// --- Start a test --------------------------------------------------------

type startAttemptRequest struct {
	LanguageCode string `json:"language_code"`
}

type startAttemptResponse struct {
	AttemptID      int64                     `json:"attempt_id"`
	TestID         int64                     `json:"test_id"`
	LanguageCode   string                    `json:"language_code"`
	TotalQuestions int                       `json:"total_questions"`
	StartedAt      time.Time                 `json:"started_at"`
	Questions      []repository.QuizQuestion `json:"questions"`
}

// startTest opens a mock test: creates an in-progress attempt and returns the
// questions in the chosen language (no answer key).
func (a *API) startTest(w http.ResponseWriter, r *http.Request) error {
	testID, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	var req startAttemptRequest
	if r.ContentLength > 0 {
		if err := httpx.Decode(r, &req); err != nil {
			return err
		}
	}
	lang, ok := validLang(req.LanguageCode)
	if !ok {
		return httpx.ErrValidation.WithDetails(map[string]any{"language_code": "must be 'en' or 'bn'"})
	}

	p, _ := middleware.PrincipalFromContext(r.Context())
	attemptID, startedAt, total, err := a.mocktest.StartAttempt(r.Context(), p.UserID, testID, lang)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			return httpx.ErrNotFound.WithDetails(map[string]any{"test_id": testID})
		case errors.Is(err, repository.ErrTestUnavailable):
			return httpx.NewAPIError(http.StatusConflict, "test_unavailable", "This test is not currently available.")
		case errors.Is(err, repository.ErrNoQuestions):
			return httpx.NewAPIError(http.StatusConflict, "no_questions", "This test has no questions yet.")
		default:
			return httpx.Wrap(httpx.ErrInternal, err)
		}
	}

	questions, err := a.mocktest.QuestionsForTest(r.Context(), testID, lang)
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}

	httpx.JSON(w, http.StatusCreated, startAttemptResponse{
		AttemptID: attemptID, TestID: testID, LanguageCode: lang,
		TotalQuestions: total, StartedAt: startedAt, Questions: questions,
	})
	return nil
}

// --- Submit a test -------------------------------------------------------

type submitRequest struct {
	Answers         []repository.SubmitAnswer `json:"answers"`
	DurationSeconds *int                      `json:"duration_seconds"`
}

// submitTest grades an attempt, stores answers + metrics, updates streak/points.
func (a *API) submitTest(w http.ResponseWriter, r *http.Request) error {
	attemptID, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	var req submitRequest
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	if req.DurationSeconds != nil && *req.DurationSeconds < 0 {
		return httpx.ErrValidation.WithDetails(map[string]any{"duration_seconds": "must be >= 0"})
	}

	p, _ := middleware.PrincipalFromContext(r.Context())
	result, err := a.mocktest.SubmitAttempt(r.Context(), p.UserID, attemptID, req.Answers, req.DurationSeconds)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrNotFound):
			return httpx.ErrNotFound.WithDetails(map[string]any{"attempt_id": attemptID})
		case errors.Is(err, repository.ErrAlreadySubmitted):
			return httpx.NewAPIError(http.StatusConflict, "already_submitted", "This attempt has already been submitted.")
		case errors.Is(err, repository.ErrInvalidAnswer):
			return httpx.ErrValidation.WithDetails(map[string]any{"answers": "an answer references an option not in its question"})
		default:
			return httpx.Wrap(httpx.ErrInternal, err)
		}
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

// --- Result, history, stats ---------------------------------------------

// attemptResult returns a completed attempt with its answer key (owner only).
func (a *API) attemptResult(w http.ResponseWriter, r *http.Request) error {
	attemptID, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	p, _ := middleware.PrincipalFromContext(r.Context())
	result, err := a.mocktest.GetResult(r.Context(), p.UserID, attemptID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return httpx.ErrNotFound.WithDetails(map[string]any{"attempt_id": attemptID})
		}
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	httpx.JSON(w, http.StatusOK, result)
	return nil
}

// myAttempts lists the caller's own attempts (history).
func (a *API) myAttempts(w http.ResponseWriter, r *http.Request) error {
	q := parseListQuery(r, attemptSort, "started_at")
	p, _ := middleware.PrincipalFromContext(r.Context())
	items, total, err := a.attempts.List(r.Context(), repository.AttemptFilter{UserID: p.UserID, TestID: queryInt64(r, "test_id")}, q.params())
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	writeList(w, q, total, items)
	return nil
}

// myStats returns the caller's analytics (points, streak, accuracy, counts).
func (a *API) myStats(w http.ResponseWriter, r *http.Request) error {
	p, _ := middleware.PrincipalFromContext(r.Context())
	stats, err := a.mocktest.Stats(r.Context(), p.UserID)
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	httpx.JSON(w, http.StatusOK, stats)
	return nil
}
