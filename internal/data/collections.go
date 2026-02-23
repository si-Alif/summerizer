package data

import (
	"database/sql"
	"time"

	"github.com/si-Alif/summerizer/internal/validator"
)

type Collection struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"user_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Version     int32     `json:"version"`
}

func ValidateCollection(v *validator.Validator, collection *Collection) {
	v.Check(collection.Title != "", "title", "must be provided")
	v.Check(len(collection.Title) <= 200, "title", "must not be more than 200 characters")

	// description is optional, but if provided must be bounded
	v.Check(len(collection.Description) <= 5000, "description", "must not be more than 5000 characters")
}

// ValidateCollectionUpdate validates a partial update (PATCH) where fields are optional.
// Only non-nil fields were provided by the client and need checking.
func ValidateCollectionUpdate(v *validator.Validator, title *string, description *string) {
	if title != nil {
		v.Check(*title != "", "title", "must not be empty")
		v.Check(len(*title) <= 200, "title", "must not be more than 200 characters")
	}
	if description != nil {
		v.Check(len(*description) <= 5000, "description", "must not be more than 5000 characters")
	}
}

type CollectionModel struct {
	DB *sql.DB
}
