package data

import (
	"database/sql"
	"time"

	"github.com/si-Alif/summerizer/internal/validator"
)

// Source represents a single learning resource (web page, YouTube video, or PDF)
// added to a collection. It tracks processing state via a two-level state machine:
//   - Status:      pending → ingesting → completed | failed → (retry) → ingesting
//   - CurrentStep: fetch → clean → chunk → embed → store
type Source struct {
	ID           int64      `json:"id"`
	CollectionID int64      `json:"collection_id"`
	SourceType   string     `json:"source_type"` // "web", "youtube", "pdf"
	URL          string     `json:"url"`
	Title        string     `json:"title"`
	Status       string     `json:"status"`       // "pending", "ingesting", "completed", "failed", "stale"
	CurrentStep  *string    `json:"current_step"` // "fetch", "clean", "chunk", "embed", "store" — nil when pending/completed
	StepError    *string    `json:"step_error"`   // error message from the last failed step
	RetryCount   int        `json:"retry_count"`
	NextRetryAt  *time.Time `json:"next_retry_at"`
	Metadata     JsonMap    `json:"metadata"` // flexible JSONB storage (page count, duration, etc.)
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	Version      int32      `json:"version"`
}

// JsonMap is a type alias for JSONB column data
type JsonMap map[string]any

// Permitted values for source status and steps — used by both validation
// and the worker state machine.
var (
	PermittedSourceTypes = []string{"web", "youtube", "pdf"}
	PermittedStatuses    = []string{"pending", "ingesting", "completed", "failed", "stale"}
	PermittedSteps       = []string{"fetch", "clean", "chunk", "embed", "store"}
)

// ValidateSource validates a source before insertion.
func ValidateSource(v *validator.Validator, source *Source) {
	v.Check(source.CollectionID > 0, "collection_id", "must be a positive integer")

	v.Check(source.URL != "", "url", "must be provided")
	v.Check(validator.ValidURL(source.URL), "url", "must be a valid HTTP or HTTPS URL")
	v.Check(len(source.URL) <= 2000, "url", "must not be more than 2000 characters")

	v.Check(source.SourceType != "", "source_type", "must be provided")
	v.Check(
		validator.PermittedValue(source.SourceType, PermittedSourceTypes...),
		"source_type",
		"must be one of: web, youtube, pdf",
	)
}

// ValidateSourceStatus validates a status transition update.
// Used by the worker when updating processing state.
func ValidateSourceStatus(v *validator.Validator, status string, currentStep *string) {
	v.Check(status != "", "status", "must be provided")
	v.Check(
		validator.PermittedValue(status, PermittedStatuses...),
		"status",
		"must be one of: pending, ingesting, completed, failed, stale",
	)

	// currentStep must be set when status is "ingesting", nil otherwise
	if status == "ingesting" {
		v.Check(currentStep != nil, "current_step", "must be set when status is ingesting")
		if currentStep != nil {
			v.Check(
				validator.PermittedValue(*currentStep, PermittedSteps...),
				"current_step",
				"must be one of: fetch, clean, chunk, embed, store",
			)
		}
	}
}

// SourceModel wraps the DB connection pool and provides
// all query methods for the sources table.
type SourceModel struct {
	DB *sql.DB
}
