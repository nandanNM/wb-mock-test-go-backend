package handler

import (
	"net/http"
	"strings"

	"backend/internal/httpx"
	"backend/internal/repository"
)

var testSort = map[string]string{"position": "position", "created_at": "created_at", "code": "test_code"}

var validScopes = map[string]bool{"chapter": true, "multi_chapter": true, "subject": true}
var validDifficulty = map[string]bool{"easy": true, "medium": true, "hard": true}

func (a *API) listTests(w http.ResponseWriter, r *http.Request) error {
	q := parseListQuery(r, testSort, "position")
	f := repository.TestFilter{SubjectID: queryInt64(r, "subject_id")}
	switch r.URL.Query().Get("published") {
	case "true":
		v := true
		f.Published = &v
	case "false":
		v := false
		f.Published = &v
	}
	items, total, err := a.tests.List(r.Context(), f, q.params())
	if err != nil {
		return httpx.Wrap(httpx.ErrInternal, err)
	}
	writeList(w, q, total, items)
	return nil
}

func (a *API) getTest(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	t, err := a.tests.Get(r.Context(), id)
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	httpx.JSON(w, http.StatusOK, t)
	return nil
}

type testCreateReq struct {
	SubjectID   int64   `json:"subject_id"`
	ScopeType   string  `json:"scope_type"`
	TitleEN     string  `json:"title_en"`
	TitleBN     string  `json:"title_bn"`
	TestCode    string  `json:"test_code"`
	Difficulty  *string `json:"difficulty"`
	Position    int     `json:"position"`
	IsPublished bool    `json:"is_published"`
	ChapterIDs  []int64 `json:"chapter_ids"`
	QuestionIDs []int64 `json:"question_ids"`
}

func validateTestCommon(fields map[string]any, scope string, difficulty *string) map[string]any {
	if scope != "" && !validScopes[scope] {
		fields = ensure(fields)
		fields["scope_type"] = "must be one of chapter, multi_chapter, subject"
	}
	if difficulty != nil && *difficulty != "" && !validDifficulty[*difficulty] {
		fields = ensure(fields)
		fields["difficulty"] = "must be one of easy, medium, hard"
	}
	return fields
}

func (a *API) createTest(w http.ResponseWriter, r *http.Request) error {
	var req testCreateReq
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	fields := requireFields(map[string]string{
		"title_en": req.TitleEN, "title_bn": req.TitleBN,
		"test_code": req.TestCode, "scope_type": req.ScopeType,
	})
	if req.SubjectID == 0 {
		fields = ensure(fields)
		fields["subject_id"] = "is required"
	}
	fields = validateTestCommon(fields, req.ScopeType, req.Difficulty)
	if fields != nil {
		return httpx.ErrValidation.WithDetails(fields)
	}

	actor, _ := principalUserID(r)
	t, err := a.tests.Create(r.Context(), repository.TestCreate{
		SubjectID: req.SubjectID, ScopeType: req.ScopeType,
		TitleEN: strings.TrimSpace(req.TitleEN), TitleBN: strings.TrimSpace(req.TitleBN),
		TestCode: strings.TrimSpace(req.TestCode), Difficulty: req.Difficulty,
		Position: req.Position, IsPublished: req.IsPublished, CreatedBy: actor,
		ChapterIDs: req.ChapterIDs, QuestionIDs: req.QuestionIDs,
	})
	if err != nil {
		return writeRepoError(err, "subject_id", req.SubjectID)
	}
	a.auditDash(r, "dashboard.test.created", map[string]any{"test_id": t.ID})
	httpx.JSON(w, http.StatusCreated, t)
	return nil
}

type testUpdateReq struct {
	TitleEN     *string `json:"title_en"`
	TitleBN     *string `json:"title_bn"`
	ScopeType   *string `json:"scope_type"`
	Difficulty  *string `json:"difficulty"`
	Position    *int    `json:"position"`
	IsPublished *bool   `json:"is_published"`
	ChapterIDs  []int64 `json:"chapter_ids"`
	QuestionIDs []int64 `json:"question_ids"`
}

func (a *API) updateTest(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	var req testUpdateReq
	if err := httpx.Decode(r, &req); err != nil {
		return err
	}
	scope := ""
	if req.ScopeType != nil {
		scope = *req.ScopeType
	}
	if fields := validateTestCommon(nil, scope, req.Difficulty); fields != nil {
		return httpx.ErrValidation.WithDetails(fields)
	}
	t, err := a.tests.Update(r.Context(), id, repository.TestUpdate{
		TitleEN: req.TitleEN, TitleBN: req.TitleBN, ScopeType: req.ScopeType,
		Difficulty: req.Difficulty, Position: req.Position, IsPublished: req.IsPublished,
		ChapterIDs: req.ChapterIDs, QuestionIDs: req.QuestionIDs,
	})
	if err != nil {
		return writeRepoError(err, "id", id)
	}
	a.auditDash(r, "dashboard.test.updated", map[string]any{"test_id": id})
	httpx.JSON(w, http.StatusOK, t)
	return nil
}

func (a *API) deleteTest(w http.ResponseWriter, r *http.Request) error {
	id, err := pathInt64(r, "id")
	if err != nil {
		return err
	}
	if err := a.tests.Delete(r.Context(), id); err != nil {
		return writeRepoError(err, "id", id)
	}
	a.auditDash(r, "dashboard.test.deleted", map[string]any{"test_id": id})
	httpx.JSON(w, http.StatusOK, map[string]any{"status": "deleted", "id": id})
	return nil
}
