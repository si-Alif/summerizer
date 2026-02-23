package data

import "github.com/si-Alif/summerizer/internal/validator"

// Filters holds pagination parameters parsed from query strings.
// Used by all list endpoints (collections, sources).
type Filters struct {
	Page     int
	PageSize int
}

// Limit returns the SQL LIMIT value.
func (f Filters) Limit() int {
	return f.PageSize
}

// Offset returns the SQL OFFSET value.
func (f Filters) Offset() int {
	return (f.Page - 1) * f.PageSize
}

// Metadata holds pagination metadata returned alongside list responses.
// Gives the client enough info to render page controls.
type Metadata struct {
	CurrentPage  int `json:"current_page,omitempty"`
	PageSize     int `json:"page_size,omitempty"`
	FirstPage    int `json:"first_page,omitempty"`
	LastPage     int `json:"last_page,omitempty"`
	TotalRecords int `json:"total_records,omitempty"`
}

// CalculateMetadata computes pagination metadata from total record count.
func CalculateMetadata(totalRecords, page, pageSize int) Metadata {
	if totalRecords == 0 {
		return Metadata{}
	}

	return Metadata{
		CurrentPage:  page,
		PageSize:     pageSize,
		FirstPage:    1,
		LastPage:     (totalRecords + pageSize - 1) / pageSize,
		TotalRecords: totalRecords,
	}
}

// ValidateFilters checks that pagination parameters are within acceptable bounds.
func ValidateFilters(v *validator.Validator, f Filters) {
	v.Check(f.Page > 0, "page", "must be greater than zero")
	v.Check(f.Page <= 10_000_000, "page", "must not exceed 10 million")
	v.Check(f.PageSize > 0, "page_size", "must be greater than zero")
	v.Check(f.PageSize <= 100, "page_size", "must not be greater than 100")
}
