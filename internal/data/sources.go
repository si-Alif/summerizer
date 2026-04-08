package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/si-Alif/summerizer/internal/validator"
)

type Source struct {
	ID           int64      `json:"id"`
	CollectionID int64      `json:"collection_id"`
	SourceType   string     `json:"source_type"`
	URL          string     `json:"url"`
	Title        string     `json:"title"`
	Status       string     `json:"status"`       // "pending", "ingesting", "completed", "failed", "stale"
	CurrentStep  *string    `json:"current_step"` // "fetch", "clean", "chunk", "embed", "store" — nil when pending/completed
	StepError    *string    `json:"step_error"`   // error message from the last failed step
	RetryCount   int        `json:"retry_count"`
	NextRetryAt  *time.Time `json:"next_retry_at"`
	Metadata     JsonMap    `json:"metadata"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	Version      int32      `json:"version"`
}

// JsonMap is a type alias for JSONB column data
type JsonMap map[string]any

func (j *JsonMap) Scan(value any) error {
	if value == nil {
		*j = make(JsonMap)
		return nil
	}

	valueBytes, ok := value.([]byte)

	if !ok {
		return fmt.Errorf("JsonMap.Scan expected []byte, got %T", value)
	}

	result := make(JsonMap)
	err := json.Unmarshal(valueBytes, &result)
	if err != nil {
		return fmt.Errorf("JsonMap.Scan error unmarshaling JSON: %w", err)
	}

	*j = result

	return nil
}


var (
	PermittedSourceTypes = []string{validator.SourceTypeWeb, validator.SourceTypeYouTube, validator.SourceTypePDF}
	PermittedStatuses    = []string{"pending", "ingesting", "completed", "failed", "stale"}
	PermittedSteps       = []string{"fetch", "clean", "chunk", "embed", "store"}
)


func ValidateSource(v *validator.Validator, source *Source) {
	v.Check(source.CollectionID > 0, "collection_id", "must be a positive integer")

	v.Check(validator.NotBlank(source.URL), "url", "must be provided")

	if validator.DetectSourceType(source.URL) != validator.SourceTypePDF {
		v.Check(validator.ValidURL(source.URL), "url", "must be a valid HTTP or HTTPS URL")
	}

	v.Check(len(source.URL) <= 2000, "url", "must not be more than 2000 characters")
	v.Check(validator.NotBlank(source.SourceType), "source_type", "must be provided")

	v.Check(
		validator.PermittedValue(source.SourceType, PermittedSourceTypes...),
		"source_type",
		"must be one of: web, youtube, pdf",
	)

	v.Check(validator.NotBlank(source.Title), "title", "must be provided")
	v.Check(len(source.Title) <= 500, "title", "must not be more than 500 characters")

	v.Check(len(source.Metadata) <= 20, "metadata", "must not have more than 20 keys")
}

// ValidateSourceStatus validates a status transition update.
// Used by the worker when updating processing state.
func ValidateSourceStatus(v *validator.Validator, status string, currentStep *string) {
	v.Check(validator.NotBlank(status), "status", "must be provided")
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


type SourceModel struct {
	DB *sql.DB
}


func (m SourceModel) Insert(source *Source) error {
	query := `
	INSERT INTO sources (collection_id, source_type, url, title, metadata)
	VALUES ($1, $2, $3, $4, $5)
	RETURNING id, status, retry_count, created_at, updated_at, version`

	args := []any{source.CollectionID, source.SourceType, source.URL, source.Title, source.Metadata}

	ctx , cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := m.DB.QueryRowContext(ctx, query, args...).Scan(
					&source.ID,
					&source.Status,
					&source.RetryCount,
					&source.CreatedAt,
					&source.UpdatedAt,
					&source.Version,
				)

		if err != nil {
		var pgErr *pgconn.PgError
		switch {
		case errors.As(err, &pgErr) && pgErr.Code == "23505":
			return ErrDuplicateRecord
		default:
			return err
		}
	}

	return nil
}

// GetByID retrieves a single source by its ID, verifying ownership via a JOIN
// on collections.user_id. Returns ErrRecordNotFound if the source doesn't exist
// or doesn't belong to the user.
func (m SourceModel) GetByID(id int64, userID int64) (*Source, error) {
	query := `
	SELECT s.id, s.collection_id, s.source_type, s.url, s.title, s.status,
			  s.current_step, s.step_error, s.retry_count, s.next_retry_at,
			  s.metadata, s.created_at, s.updated_at, s.version
	FROM sources s
	JOIN collections c ON s.collection_id = c.id
	WHERE s.id = $1 AND c.user_id = $2`

	var source Source

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := m.DB.QueryRowContext(ctx, query, id, userID).Scan(
		&source.ID,
		&source.CollectionID,
		&source.SourceType,
		&source.URL,
		&source.Title,
		&source.Status,
		&source.CurrentStep,
		&source.StepError,
		&source.RetryCount,
		&source.NextRetryAt,
		&source.Metadata,
		&source.CreatedAt,
		&source.UpdatedAt,
		&source.Version,
	)

	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, ErrRecordNotFound
		default:
			return nil, err
		}
	}

	return &source, nil
}


func (m SourceModel) GetAllByCollection(collectionID int64, userID int64, status string, filters Filters) ([]*Source, Metadata, error) {
	query := fmt.Sprintf(`
	SELECT count(*) OVER(), s.id, s.collection_id, s.source_type, s.url, s.title, s.status,
			  s.current_step, s.step_error, s.retry_count, s.next_retry_at,
			  s.metadata, s.created_at, s.updated_at, s.version
	FROM sources s
	JOIN collections c ON s.collection_id = c.id
	WHERE s.collection_id = $1 AND c.user_id = $2
	AND (s.status = $3 OR $3 = '')
	ORDER BY %s %s, s.id ASC
	LIMIT $4 OFFSET $5`, filters.SortColumn(), filters.SortDirection())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rows, err := m.DB.QueryContext(ctx, query, collectionID, userID, status, filters.Limit(), filters.Offset())
	if err != nil {
		return nil, Metadata{}, err
	}
	defer rows.Close()

	totalRecords := 0
	sources := []*Source{}

	for rows.Next() {
		var source Source

		err := rows.Scan(
			&totalRecords,
			&source.ID,
			&source.CollectionID,
			&source.SourceType,
			&source.URL,
			&source.Title,
			&source.Status,
			&source.CurrentStep,
			&source.StepError,
			&source.RetryCount,
			&source.NextRetryAt,
			&source.Metadata,
			&source.CreatedAt,
			&source.UpdatedAt,
			&source.Version,
		)

		if err != nil {
			return nil, Metadata{}, err
		}

		sources = append(sources, &source)
	}

	if err = rows.Err(); err != nil {
		return nil, Metadata{}, err
	}

	meta := CalculateMetadata(totalRecords, filters.Page, filters.PageSize)

	return sources, meta, nil
}


func (m SourceModel) Delete(id int64, userID int64) error {
	query := `
	DELETE FROM sources s USING collections c
	WHERE s.collection_id = c.id AND s.id = $1 AND c.user_id = $2`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := m.DB.ExecContext(ctx, query, id, userID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return ErrRecordNotFound
	}

	return nil
}


// CountByCollection returns the total number of sources in a collection, used for pagination metadata.
func (m SourceModel) CountByCollection(collectionID int64) (int, error) {
	query := `SELECT COUNT(*) FROM sources WHERE collection_id = $1`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var count int
	err := m.DB.QueryRowContext(ctx, query, collectionID).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}


func (m SourceModel) GetStatusSummary(collectionID int64) (map[string]int, error) {

	query := `SELECT status, COUNT(*) FROM sources WHERE collection_id = $1 GROUP BY status`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rows, err := m.DB.QueryContext(ctx, query, collectionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	statusSummary := make(map[string]int)

	for rows.Next() {
		var status string
		var count int

		err := rows.Scan(&status, &count)
		if err != nil {
			return nil, err
		}

		statusSummary[status] = count
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}


	for _, s := range PermittedStatuses {
		if _, exists := statusSummary[s]; !exists {
			statusSummary[s] = 0
		}
	}

	return statusSummary, nil
}

// ---------------------------------------------------------------------------
// Worker methods — used by the background ingestion pipeline, not the API.
// ---------------------------------------------------------------------------

func (m SourceModel) ClaimPending(limit int) ([]*Source, error) {
	ctx , cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tx , err := m.DB.BeginTx(ctx , nil)
	if err != nil {
		return nil, err
	}

	defer tx.Rollback()

	selectQuery := `
	SELECT id, collection_id, source_type, url, title, status,
				current_step, step_error, retry_count, next_retry_at,
				metadata, created_at, updated_at, version
	FROM sources
	WHERE status IN ('pending', 'failed')
		AND (retry_count < 5 OR retry_count IS NULL)
		AND (next_retry_at IS NULL OR next_retry_at <= now())
	ORDER BY next_retry_at ASC NULLS FIRST
	FOR UPDATE SKIP LOCKED
	LIMIT $1`

	rows, err := tx.QueryContext(ctx, selectQuery, limit)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var sources []*Source
	var ids []int64

	for rows.Next() {
		var source Source

		err := rows.Scan(
			&source.ID,
			&source.CollectionID,
			&source.SourceType,
			&source.URL,
			&source.Title,
			&source.Status,
			&source.CurrentStep,
			&source.StepError,
			&source.RetryCount,
			&source.NextRetryAt,
			&source.Metadata,
			&source.CreatedAt,
			&source.UpdatedAt,
			&source.Version,
		)

		if err != nil {
			return nil, err
		}

		sources = append(sources, &source)
		ids = append(ids, source.ID)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	if len(ids) == 0 {
		return []*Source{}, tx.Commit()
	}

	updateQuery := `
	UPDATE sources
	SET status = 'ingesting',
			current_step = 'fetch',
			step_error = NULL,
			next_retry_at = NULL,
			version = version + 1
	WHERE id = ANY($1)`

	_, err = tx.ExecContext(ctx, updateQuery, ids)
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	step := "fetch"

	for _, s := range sources {
			s.Status = "ingesting"
			s.CurrentStep = &step
			s.StepError = nil
			s.NextRetryAt = nil
			s.Version++
			s.UpdatedAt = time.Now()
	}

	return sources, nil

}


func (m SourceModel) UpdateStatus(id int64, status string, currentStep string) error {

	query := `
	UPDATE sources
	SET
		status = $1,
		current_step = $2,
		version = version + 1
	WHERE id = $3`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	args := []any{status, currentStep, id}
	_, err := m.DB.ExecContext(ctx, query, args...)

	if err != nil {
		return  err
	}

	return nil
}


func (m SourceModel) MarkAsFailed(id int64, step string, errMsg string) error {
	query := `
	UPDATE sources
	SET
		status = 'failed',
		current_step = $1,
		step_error = $2,
		retry_count = retry_count + 1,
		next_retry_at = now() + (interval '1 minute' * power(2, retry_count)),
		version = version + 1
	WHERE id = $3
	RETURNING retry_count`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	args := []any{step, errMsg, id}
	var retryCount int
	err := m.DB.QueryRowContext(ctx, query, args...).Scan(&retryCount)

	if err != nil {
		return err
	}

	if retryCount > 5 {
		updateQuery := `
		UPDATE sources
		SET status = 'stale', version = version + 1
		WHERE id = $1`

	_, updateErr := m.DB.ExecContext(ctx, updateQuery, id)
		if updateErr != nil {
			return fmt.Errorf("marking source as stale after max retries: %w", updateErr)
		}
	}

	return  err
}