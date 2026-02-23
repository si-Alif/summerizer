package data

import (
	"database/sql"
	"time"

	"github.com/si-Alif/summerizer/internal/validator"
)

// User represents a registered user of the application.
// The password_hash is never exposed in JSON responses.
type User struct {
	ID           int64     `json:"id"`
	Email        string    `json:"email"`
	PasswordHash []byte    `json:"-"` // never included in JSON output
	FirstName    string    `json:"first_name"`
	LastName     string    `json:"last_name"`
	Activated    bool      `json:"activated"`
	CreatedAt    time.Time `json:"created_at"`
	Version      int32     `json:"version"`
}

// AnonymousUser is a sentinel value for unauthenticated requests.
// Middleware sets this when no valid token is provided.
var AnonymousUser = &User{}

// IsAnonymous returns true if the user is the anonymous sentinel.
func (u *User) IsAnonymous() bool {
	return u == AnonymousUser
}

// ValidateEmail runs all email-related checks against the validator.
func ValidateEmail(v *validator.Validator, email string) {
	v.Check(validator.NotBlank(email), "email", "must be provided")
	v.Check(v.Matches(email, *validator.EmailRX), "email", "must be a valid email address")
}

// ValidatePasswordPlaintext checks password strength requirements.
func ValidatePasswordPlaintext(v *validator.Validator, password string) {
	v.Check(password != "", "password", "must be provided")
	v.Check(len(password) >= 8, "password", "must be at least 8 characters long")
	v.Check(len(password) <= 72, "password", "must not be more than 72 characters long")
}

// ValidateUser runs full validation for user registration.
func ValidateUser(v *validator.Validator, user *User) {
	v.Check(user.FirstName != "", "first_name", "must be provided")
	v.Check(len(user.FirstName) <= 200, "first_name", "must not be more than 200 characters")

	v.Check(user.LastName != "", "last_name", "must be provided")
	v.Check(len(user.LastName) <= 200, "last_name", "must not be more than 200 characters")

	ValidateEmail(v, user.Email)
}

// ValidateLoginInput validates the login request fields.
// Password plaintext validation is separate because login only needs
// "not empty" — strength rules only apply during registration.
func ValidateLoginInput(v *validator.Validator, email, password string) {
	ValidateEmail(v, email)
	v.Check(password != "", "password", "must be provided")
}

// UserModel wraps the DB connection pool and provides
// all query methods for the users table.
type UserModel struct {
	DB *sql.DB
}
