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

type Collection struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Max_Sources int       `json:"max_sources"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Version     int32     `json:"version"`
}

func ValidateCollection(v *validator.Validator, collection *Collection) {
	v.Check(validator.NotBlank(collection.Title), "title", "must be provided")
	v.Check(len(collection.Title) <= 200, "title", "must not be more than 200 characters")

	v.Check(collection.Max_Sources > 0, "max_sources", "must be greater than 0")
	v.Check(collection.Max_Sources <= 100, "max_sources", "must not be more than 100")

	v.Check(len(collection.Description) <= 5000, "description", "must not be more than 5000 characters")
}

type CollectionModel struct {
	DB *sql.DB
}

func (m CollectionModel) Insert(collection *Collection) error {
	query := `
	INSERT INTO collections (user_id, title, description, max_sources)
	VALUES ($1, $2, $3, $4)
	RETURNING id, created_at, updated_at, version`

	args := []any{collection.UserID, collection.Title, collection.Description, collection.Max_Sources}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := m.DB.QueryRowContext(ctx, query, args...).Scan(&collection.ID, &collection.CreatedAt, &collection.UpdatedAt, &collection.Version)

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

func (m CollectionModel) GetByID(id int64, userId int64) (*Collection, error) {
	query := `
	SELECT id, user_id, title, description, max_sources, created_at, updated_at, version
	FROM collections
	WHERE id = $1 AND user_id = $2`

	var collection Collection

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := m.DB.QueryRowContext(ctx, query, id, userId).Scan(
		&collection.ID,
		&collection.UserID,
		&collection.Title,
		&collection.Description,
		&collection.Max_Sources,
		&collection.CreatedAt,
		&collection.UpdatedAt,
		&collection.Version,
	)

	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, ErrRecordNotFound
		default:
			return nil, err
		}
	}

	return &collection, nil
}

func (m CollectionModel) GetAll(userID int64, title string, filters Filters) ([]*Collection, Metadata, error) {
	query := fmt.Sprintf(`
	SELECT count(*) OVER(), id, user_id, title, description, max_sources, created_at, updated_at, version
	FROM collections
	WHERE user_id = $1
	AND (to_tsvector('simple',title) @@ plainto_tsquery('simple' , $2) OR $2='')
	ORDER BY %s %s , id ASC
	LIMIT $3 OFFSET $4`, filters.SortColumn(), filters.SortDirection())

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	rows, err := m.DB.QueryContext(ctx, query, userID, title, filters.Limit(), filters.Offset())

	if err != nil {
		return nil, Metadata{}, err
	}
	defer rows.Close() // close the stream of data from the database once we are done with it

	collections := []*Collection{}
	totalRecords := 0

	for rows.Next() {
		var collection Collection

		err := rows.Scan(
			&totalRecords,
			&collection.ID,
			&collection.UserID,
			&collection.Title,
			&collection.Description,
			&collection.Max_Sources,
			&collection.CreatedAt,
			&collection.UpdatedAt,
			&collection.Version,
		)

		if err != nil {
			return nil, Metadata{}, err
		}

		collections = append(collections, &collection)
	}

	if err = rows.Err(); err != nil {
		return nil, Metadata{}, err
	}

	metadata := CalculateMetadata(totalRecords, filters.Page, filters.PageSize)

	return collections, metadata, nil
}

func (m CollectionModel) Update(collection *Collection) error {
	query := `
	UPDATE collections
	SET title = $1, description = $2, max_sources = $3, version = version + 1
	WHERE id = $4 AND user_id = $5 AND version = $6
	RETURNING updated_at, version`

	args := []any{collection.Title, collection.Description, collection.Max_Sources, collection.ID, collection.UserID, collection.Version}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := m.DB.QueryRowContext(ctx, query, args...).Scan(&collection.UpdatedAt, &collection.Version)

	if err != nil {
		var pgErr *pgconn.PgError
		switch {
		case errors.Is(err, sql.ErrNoRows):
				return ErrEditConflict
		case errors.As(err, &pgErr) && pgErr.Code == "23505":
				return ErrDuplicateRecord
		default:
				return err
		}
	}

	return nil

}

func (m CollectionModel) Delete(id int64, userID int64) error {
	query := `DELETE FROM collections WHERE id = $1 AND user_id = $2`

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
