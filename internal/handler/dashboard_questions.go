package handler

import (
	"net/http"
	"strings"

	"backend/internal/httpx"
	"backend/internal/repository"
)

var questionSort = map[string]string{"position": "position", "created_at": "created_at"}

func (a *API) listQuestions(w http.ResponseWriter, r *http.Request) error {
	q := parseListQuery(r, questionSort, "position")
	items, total, err := a.questions.List(r.Context(), queryInt64(r, "chapter_id"), q.params())
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	writeList(w, q, total, items)
	return nil
}

func (a *API) getQuestion(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	qq, err := a.questions.Get(r.Context(), id)
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	httpx.JSON(w, http.StatusOK, qq)
	return nil
}

type optionReq struct {
	Position  int16  `json:"position"`
	BodyEN    string `json:"body_en"`
	BodyBN    string `json:"body_bn"`
	IsCorrect bool   `json:"is_correct"`
}

type questionCreateReq struct {
	ChapterID     int64       `json:"chapter_id"`
	PromptEN      string      `json:"prompt_en"`
	PromptBN      string      `json:"prompt_bn"`
	ExplanationEN *string     `json:"explanation_en"`
	ExplanationBN *string     `json:"explanation_bn"`
	Position      int         `json:"position"`
	Options       []optionReq `json:"options"`
}

// validateOptions mirrors the DB constraint (>=2 options, exactly 1 correct) so
// the client gets a clear 422 instead of a generic DB error.
func validateOptions(opts []optionReq) map[string]any {
	if len(opts) < 2 {
		return map[string]any{"options": "at least 2 options are required"}
	}
	correct := 0
	for _, o := range opts {
		if o.IsCorrect {
			correct++
		}
		if strings.TrimSpace(o.BodyEN) == "" || strings.TrimSpace(o.BodyBN) == "" {
			return map[string]any{"options": "each option needs body_en and body_bn"}
		}
	}
	if correct != 1 {
		return map[string]any{"options": "exactly one option must be marked correct"}
	}
	return nil
}

func toOptionInputs(opts []optionReq) []repository.OptionInput {
	out := make([]repository.OptionInput, len(opts))
	for i, o := range opts {
		out[i] = repository.OptionInput{Position: o.Position, BodyEN: o.BodyEN, BodyBN: o.BodyBN, IsCorrect: o.IsCorrect}
	}
	return out
}

func (a *API) createQuestion(w http.ResponseWriter, r *http.Request) error {
	var req questionCreateReq
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	fields := requireFields(map[string]string{"prompt_en": req.PromptEN, "prompt_bn": req.PromptBN})
	if req.ChapterID == 0 {
		fields = ensure(fields)
		fields["chapter_id"] = "is required"
	}
	if optErr := validateOptions(req.Options); optErr != nil {
		fields = ensure(fields)
		for k, v := range optErr {
			fields[k] = v
		}
	}
	if fields != nil {
		return httpx.ErrValidation.WithDetails(fields)
	}

	actor, _ := principalUserID(r)
	qq, err := a.questions.Create(r.Context(), repository.QuestionCreate{
		ChapterID: req.ChapterID, PromptEN: req.PromptEN, PromptBN: req.PromptBN,
		ExplanationEN: req.ExplanationEN, ExplanationBN: req.ExplanationBN,
		Position: req.Position, CreatedBy: actor, Options: toOptionInputs(req.Options),
	})
	if err != nil {
		return writeRepoError(err, "chapter_id", req.ChapterID)
	}
	a.auditDash(r, "dashboard.question.created", map[string]any{"question_id": qq.ID})
	httpx.JSON(w, http.StatusCreated, qq)
	return nil
}

type questionUpdateReq struct {
	PromptEN      *string     `json:"prompt_en"`
	PromptBN      *string     `json:"prompt_bn"`
	ExplanationEN *string     `json:"explanation_en"`
	ExplanationBN *string     `json:"explanation_bn"`
	Position      *int        `json:"position"`
	Options       []optionReq `json:"options"` // omit to leave unchanged
}

func (a *API) updateQuestion(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	var req questionUpdateReq
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	u := repository.QuestionUpdate{
		PromptEN: req.PromptEN, PromptBN: req.PromptBN,
		ExplanationEN: req.ExplanationEN, ExplanationBN: req.ExplanationBN, Position: req.Position,
	}
	if req.Options != nil { // full replace requested -> validate
		if optErr := validateOptions(req.Options); optErr != nil {
			return httpx.ErrValidation.WithDetails(optErr)
		}
		u.Options = toOptionInputs(req.Options)
	}
	qq, err := a.questions.Update(r.Context(), id, u)
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	a.auditDash(r, "dashboard.question.updated", map[string]any{"question_id": id})
	httpx.JSON(w, http.StatusOK, qq)
	return nil
}

func (a *API) deleteQuestion(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	if err := a.questions.Delete(r.Context(), id); err != nil {
		return writeRepoError(err, "id", id)
	}
	a.auditDash(r, "dashboard.question.deleted", map[string]any{"question_id": id})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
	return nil
}
