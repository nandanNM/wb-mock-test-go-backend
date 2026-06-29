package repository

import "errors"

// ErrInUse is returned when a row can't be deleted because other rows reference
// it (a foreign-key RESTRICT violation).
var ErrInUse = errors.New("record is referenced by other records")

// ListParams carries pagination/sort for list queries. Sort must already be a
// whitelisted SQL column and Order must be "ASC" or "DESC" — the handler layer
// guarantees this (see parseListQuery), so they are safe to interpolate.
type ListParams struct {
	Limit  int
	Offset int
	Sort   string
	Order  string
	Search string
}

// orderBy builds a safe "ORDER BY <col> <dir>" clause, falling back to
// defaultCol/DESC if unset.
func (p ListParams) orderBy(defaultCol string) string {
	col := p.Sort
	if col == "" {
		col = defaultCol
	}
	dir := p.Order
	if dir != "ASC" && dir != "DESC" {
		dir = "DESC"
	}
	return "ORDER BY " + col + " " + dir
}

func (p ListParams) limit() int {
	if p.Limit <= 0 {
		return 20
	}
	return p.Limit
}
