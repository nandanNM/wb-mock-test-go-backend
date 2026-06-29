package handler

import (
	"net/http"
	"strconv"
	"strings"

	"backend/internal/httpx"
	"backend/internal/repository"
)

// listQuery is the parsed, validated set of list parameters shared by every
// dashboard list endpoint: pagination, sorting, and free-text search.
type listQuery struct {
	Page   int
	Limit  int
	Offset int
	Sort   string // a whitelisted SQL column (safe to interpolate)
	Order  string // "ASC" or "DESC" (literal, safe to interpolate)
	Search string
}

const (
	defaultLimit = 20
	maxLimit     = 100
)

// parseListQuery reads ?page, ?limit, ?sort, ?order, ?search. The sort field is
// resolved against `sortable` (API name -> SQL column); unknown/absent sorts
// fall back to defaultSort. This whitelist is what makes Sort safe to
// interpolate into SQL.
func parseListQuery(r *http.Request, sortable map[string]string, defaultSort string) listQuery {
	q := r.URL.Query()

	page := atoiDefault(q.Get("page"), 1)
	if page < 1 {
		page = 1
	}
	limit := atoiDefault(q.Get("limit"), defaultLimit)
	if limit < 1 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}

	sortCol := defaultSort
	if col, ok := sortable[q.Get("sort")]; ok {
		sortCol = col
	}
	order := "DESC"
	if strings.EqualFold(q.Get("order"), "asc") {
		order = "ASC"
	}

	return listQuery{
		Page:   page,
		Limit:  limit,
		Offset: (page - 1) * limit,
		Sort:   sortCol,
		Order:  order,
		Search: strings.TrimSpace(q.Get("search")),
	}
}

// listResult is the paginated list envelope (sent inside httpx's {data:...}).
type listResult struct {
	Items      any   `json:"items"`
	Page       int   `json:"page"`
	Limit      int   `json:"limit"`
	Total      int64 `json:"total"`
	TotalPages int64 `json:"total_pages"`
}

// writeList renders a paginated list response.
func writeList(w http.ResponseWriter, q listQuery, total int64, items any) {
	totalPages := int64(0)
	if total > 0 {
		totalPages = (total + int64(q.Limit) - 1) / int64(q.Limit)
	}
	httpx.JSON(w, http.StatusOK, listResult{
		Items:      items,
		Page:       q.Page,
		Limit:      q.Limit,
		Total:      total,
		TotalPages: totalPages,
	})
}

// params converts a parsed listQuery into the repository-layer ListParams.
func (q listQuery) params() repository.ListParams {
	return repository.ListParams{
		Limit:  q.Limit,
		Offset: q.Offset,
		Sort:   q.Sort,
		Order:  q.Order,
		Search: q.Search,
	}
}

// pathInt64 parses a numeric path value (e.g. {id}), returning a client-safe
// bad-request error when it isn't a valid integer.
func pathInt64(r *http.Request, name string) (int64, error) {
	v, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		return 0, httpx.ErrBadRequest.WithDetails(map[string]any{name: "must be an integer"})
	}
	return v, nil
}

// queryInt64 reads an optional numeric query param (0 if absent/invalid).
func queryInt64(r *http.Request, name string) int64 {
	v, _ := strconv.ParseInt(r.URL.Query().Get(name), 10, 64)
	return v
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return def
}
