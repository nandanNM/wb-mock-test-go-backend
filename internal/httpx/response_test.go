package httpx

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestErrorMapsAPIError(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)

	Error(rec, req, ErrNotFound.WithDetails(map[string]any{"id": "9"}))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404", rec.Code)
	}
	var body struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != "not_found" {
		t.Errorf("code: got %q, want not_found", body.Error.Code)
	}
	if body.Error.Details["id"] != "9" {
		t.Errorf("details.id: got %v, want 9", body.Error.Details["id"])
	}
}

func TestErrorUnknownBecomes500(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)

	Error(rec, req, errors.New("boom"))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", rec.Code)
	}
	// Internal cause must not leak to the client.
	if got := rec.Body.String(); contains(got, "boom") {
		t.Errorf("internal error leaked to client: %s", got)
	}
}

func TestHandlerRendersReturnedError(t *testing.T) {
	h := Handler(func(w http.ResponseWriter, r *http.Request) error {
		return ErrForbidden
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status: got %d, want 403", rec.Code)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
