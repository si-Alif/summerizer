package data

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/si-Alif/summerizer/internal/validator"
)

const (
	EmbeddingJobStatusPending    = "pending"
	EmbeddingJobStatusProcessing = "processing"
	EmbeddingJobStatusCompleted  = "completed"
	EmbeddingJobStatusFailed     = "failed"
	EmbeddingJobStatusDead       = "dead"
	defaultEmbeddingMaxAttempts  = 5
)

type EmbeddingJob struct {
	ID            int64      `json:"id"`
	SourceID      int64      `json:"source_id"`
	SourceVersion int        `json:"source_version"`
	Status        string     `json:"status"`
	Attempts      int        `json:"attempts"`
	MaxAttempts   int        `json:"max_attempts"`
	RunAfter      time.Time  `json:"run_after"`
	LockedAt      *time.Time `json:"locked_at,omitzero"`
	LockedBy      *string    `json:"locked_by,omitzero"`
	LastError     *string    `json:"last_error,omitzero"`
	Version       int32      `json:"version"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

var (
	PermittedEmbeddingJobStatuses = []string{
		EmbeddingJobStatusPending,
		EmbeddingJobStatusProcessing,
		EmbeddingJobStatusCompleted,
		EmbeddingJobStatusFailed,
		EmbeddingJobStatusDead,
	}
)

func ValidateEmbeddingJob(v *validator.Validator, job *EmbeddingJob) {
	v.Check(job.SourceID > 0, "source_id", "must be provided and greater than 0")
	v.Check(job.SourceVersion > 0, "source_version", "must be provided and greater than 0")
	v.Check(validator.NotBlank(job.Status), "status", "must be provided")
	v.Check(validator.PermittedValue(job.Status, PermittedEmbeddingJobStatuses...), "status", "must be one of the permitted statuses")
	v.Check(job.Attempts >= 0, "attempts", "must be non-negative")
	v.Check(job.MaxAttempts > 0, "max_attempts", "must be greater than 0")
}

type EmbeddingJobModel struct {
	DB *sql.DB
}

func (m EmbeddingJobModel) Insert(job *EmbeddingJob) error {
	if job.Status == "" {
		job.Status = EmbeddingJobStatusPending
	}

	if job.MaxAttempts <= 0 {
		job.MaxAttempts = defaultEmbeddingMaxAttempts
	}

	if job.RunAfter.IsZero() {
		job.RunAfter = time.Now()
	}

	query := `
	INSERT INTO embedding_jobs (source_id, source_version, status, attempts, max_attempts, run_after)
	VALUES ($1, $2, $3, $4, $5, $6)
	RETURNING id, status, attempts, max_attempts, run_after, version, created_at, updated_at
	`

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	args := []any{
		job.SourceID,
		job.SourceVersion,
		job.Status,
		job.Attempts,
		job.MaxAttempts,
		job.RunAfter,
	}

	err := m.DB.QueryRowContext(ctx, query, args...).Scan(
		&job.ID,
		&job.Status,
		&job.Attempts,
		&job.MaxAttempts,
		&job.RunAfter,
		&job.Version,
		&job.CreatedAt,
		&job.UpdatedAt,
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

func (m EmbeddingJobModel) ClaimPending(limit int, lockedBy string) ([]*EmbeddingJob, error) {
	if limit <= 0 {
		limit = 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tx, err := m.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	selectQuery := `
	SELECT id, source_id, source_version, status, attempts, max_attempts,
			 run_after, locked_at, locked_by, last_error, version, created_at, updated_at
	FROM embedding_jobs
	WHERE status IN ('pending', 'failed')
		AND attempts < max_attempts
		AND run_after <= now()
	ORDER BY run_after ASC, id ASC
	FOR UPDATE SKIP LOCKED
	LIMIT $1`

	rows, err := tx.QueryContext(ctx, selectQuery, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*EmbeddingJob
	var ids []int64

	for rows.Next() {
		var job EmbeddingJob
		var lockedAt sql.NullTime
		var lockedByValue sql.NullString
		var lastErrorValue sql.NullString

		err := rows.Scan(
			&job.ID,
			&job.SourceID,
			&job.SourceVersion,
			&job.Status,
			&job.Attempts,
			&job.MaxAttempts,
			&job.RunAfter,
			&lockedAt,
			&lockedByValue,
			&lastErrorValue,
			&job.Version,
			&job.CreatedAt,
			&job.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}

		if lockedAt.Valid {
			job.LockedAt = &lockedAt.Time
		}

		if lockedByValue.Valid {
			lockedByCopy := lockedByValue.String
			job.LockedBy = &lockedByCopy
		}

		if lastErrorValue.Valid {
			lastErrorCopy := lastErrorValue.String
			job.LastError = &lastErrorCopy
		}

		jobs = append(jobs, &job)
		ids = append(ids, job.ID)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	if len(ids) == 0 {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return []*EmbeddingJob{}, nil
	}

	updateQuery := `
	UPDATE embedding_jobs
	SET status = 'processing',
			locked_at = now(),
			locked_by = NULLIF($2, ''),
			last_error = NULL,
			version = version + 1
	WHERE id = ANY($1)`

	if _, err = tx.ExecContext(ctx, updateQuery, ids, lockedBy); err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	now := time.Now()
	for _, job := range jobs {
		job.Status = EmbeddingJobStatusProcessing
		job.LastError = nil
		job.Version++
		job.LockedAt = &now
		if lockedBy == "" {
			job.LockedBy = nil
		} else {
			worker := lockedBy
			job.LockedBy = &worker
		}
	}

	return jobs, nil
}

func (m EmbeddingJobModel) MarkAsCompleted(id int64, ver int32, lockedBy string) (int32, error) {
	query := `
	UPDATE embedding_jobs
	SET status = 'completed',
			locked_at = NULL,
			locked_by = NULL,
			last_error = NULL,
			version = version + 1
	WHERE id = $1
		AND version = $2
		AND status = 'processing'
		AND (locked_by IS NULL OR locked_by = NULLIF($3, ''))
	RETURNING version`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var newVersion int32
	err := m.DB.QueryRowContext(ctx, query, id, ver, lockedBy).Scan(&newVersion)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrEditConflict
		}
		return 0, err
	}

	return newVersion, nil
}

func (m EmbeddingJobModel) MarkAsFailed(id int64, errMsg string, ver int32, lockedBy string) error {
	query := `
	UPDATE embedding_jobs
	SET status = 'failed',
			attempts = attempts + 1,
			last_error = $1,
			run_after = now() + (interval '1 minute' * power(2, attempts)),
			locked_at = NULL,
			locked_by = NULL,
			version = version + 1
	WHERE id = $2
		AND version = $3
		AND status = 'processing'
		AND (locked_by IS NULL OR locked_by = NULLIF($4, ''))
	RETURNING attempts, max_attempts, version`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var attempts int
	var maxAttempts int
	var newVersion int32
	err := m.DB.QueryRowContext(ctx, query, errMsg, id, ver, lockedBy).Scan(&attempts, &maxAttempts, &newVersion)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrEditConflict
		}
		return err
	}

	if attempts >= maxAttempts {
		deadQuery := `
		UPDATE embedding_jobs
		SET status = 'dead',
				locked_at = NULL,
				locked_by = NULL,
				version = version + 1
		WHERE id = $1 AND version = $2`

		if _, deadErr := m.DB.ExecContext(ctx, deadQuery, id, newVersion); deadErr != nil {
			return fmt.Errorf("marking embedding job as dead after max attempts: %w", deadErr)
		}
	}

	return nil
}

func (m EmbeddingJobModel) ReclaimStuckAtProcessing(threshold time.Duration) (int64, error) {
	query := `
	WITH stuck_jobs AS (
		SELECT
			id,
			COALESCE(attempts, 0) + 1 AS new_attempts,
			max_attempts
		FROM embedding_jobs
		WHERE status = 'processing'
			AND locked_at IS NOT NULL
			AND locked_at < now() - $1 * INTERVAL '1 second'
	)
	UPDATE embedding_jobs ej
	SET attempts = sj.new_attempts,
			status = CASE
				WHEN sj.new_attempts >= sj.max_attempts THEN 'dead'
				ELSE 'failed'
			END,
			run_after = CASE
				WHEN sj.new_attempts >= sj.max_attempts THEN now()
				ELSE now() + (interval '1 minute' * power(2, sj.new_attempts - 1))
			END,
			locked_at = NULL,
			locked_by = NULL,
			last_error = 'auto-recovered stuck embedding job',
			version = version + 1
	FROM stuck_jobs sj
	WHERE ej.id = sj.id`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := m.DB.ExecContext(ctx, query, int(threshold.Seconds()))
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}
