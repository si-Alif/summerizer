package data

import (
	"strings"

	"github.com/si-Alif/summerizer/internal/validator"
)

type Filters struct {
	Page     int
	PageSize int
	Sort string
	SortSafeList []string
}

type Metadata struct {
	CurrentPage  int `json:"current_page,omitzero"`
	PageSize     int `json:"page_size,omitzero"`
	FirstPage    int `json:"first_page,omitzero"`
	LastPage     int `json:"last_page,omitzero"`
	TotalRecords int `json:"total_records,omitzero"`
}


// Limit returns the SQL LIMIT value.
func (f Filters) Limit() int {
	return f.PageSize
}

// Offset returns the SQL OFFSET value.
func (f Filters) Offset() int {
	return (f.Page - 1) * f.PageSize
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

	v.Check(validator.PermittedValue(f.Sort, f.SortSafeList...), "sort", "must be a permitted sort value")
}

// SORT BY <column> value extraction
func (f Filters) SortColumn() string {
	for _ , safeVal := range f.SortSafeList{
		if f.Sort == safeVal {
			return  strings.TrimPrefix(f.Sort , "-")
		}
	}

	panic("unsafe sort value: " + f.Sort)
}

// SortDirection returns "ASC" or "DESC" based on the presence of a "-" prefix in the Sort field.
func (f Filters) SortDirection() string {
	if strings.HasPrefix(f.Sort , "-") {
		return "DESC"
	}

	return "ASC"
}
