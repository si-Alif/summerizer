package data

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/si-Alif/summerizer/internal/validator"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrDuplicateEmail = errors.New("duplicate email")
)

type User struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Password  password  `json:"-"`
	Fullname  string    `json:"fullname"`
	Activated bool      `json:"activated"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int32     `json:"-"`
}

type password struct {
	plaintext *string
	hash      []byte
}

// for user's who aren't authenticated , this is the value they would be given instead of a real user instance
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

	v.Check(user.Fullname != "", "fullname", "must be provided")
	v.Check(len(user.Fullname) <= 200, "fullname", "must not be more than 200 characters")

	ValidateEmail(v, user.Email)

	if user.Password.plaintext != nil {
		ValidatePasswordPlaintext(v, *user.Password.plaintext)
	}

	if user.Password.hash == nil {
		panic("missing password hash for user")
	}
}

// ValidateLoginInput validates the login request fields.
func ValidateLoginInput(v *validator.Validator, email, password string) {
	ValidateEmail(v, email)
	v.Check(password != "", "password", "must be provided")
}

type UserModel struct {
	DB *sql.DB
}

func (p *password) Set(plaintextPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintextPassword), 12)
	if err != nil {
		return err
	}

	p.plaintext = &plaintextPassword
	p.hash = hash
	return nil

}

func (p *password) Matches(plaintextPassword string) (bool, error) {
	err := bcrypt.CompareHashAndPassword(p.hash, []byte(plaintextPassword))
	if err != nil {
		switch {
		case errors.Is(err, bcrypt.ErrMismatchedHashAndPassword):
			return false, nil
		default:
			return false, err
		}
	}

	return true, nil
}

// DB interaction methods for the UserModel
func (m *UserModel) Insert(user *User) error {
	query := `
	INSERT INTO users (email, password_hash, fullname, activated)
	VALUES ($1, $2, $3, $4)
	RETURNING id, created_at ,updated_at, version
	`

	args := []any{user.Email, user.Password.hash, user.Fullname, user.Activated}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := m.DB.QueryRowContext(ctx, query, args...).Scan(
		&user.ID,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.Version,
	)

	if err != nil {
		var pgErr *pgconn.PgError
		switch {
		case errors.As(err, &pgErr) && pgErr.Code == "23505":
			return ErrDuplicateEmail
		default:
			return err
		}
	}

	return nil

}

// while performing email login , fetch user via email and then compare the password hash with the provided password
func (m *UserModel) GetByEmail(email string) (*User, error) {
	query := `
	SELECT id, email, password_hash, fullname, activated, created_at ,updated_at , version
	FROM users
	WHERE email = $1
	`

	var user User

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := m.DB.QueryRowContext(ctx, query, email).Scan(
		&user.ID,
		&user.Email,
		&user.Password.hash,
		&user.Fullname,
		&user.Activated,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.Version,
	)

	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, ErrRecordNotFound
		default:
			return nil, err
		}
	}

	return &user, nil
}

func (m *UserModel) GetByID(id int64) (*User, error) {
	query := `
	SELECT id, email, fullname, activated, created_at ,updated_at , version
	FROM users
	WHERE id = $1
	`

	var user User

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := m.DB.QueryRowContext(ctx, query, id).Scan(
		&user.ID,
		&user.Email,
		&user.Fullname,
		&user.Activated,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.Version,
	)

	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, ErrRecordNotFound
		default:
			return nil, err
		}
	}

	return &user, nil
}

func (m *UserModel) Update(user *User) error {
	query := `UPDATE users SET email = $1, fullname = $2, activated = $3, password_hash = $4, version = version + 1
	WHERE id = $5 AND version = $6
	RETURNING version`

	args := []any{user.Email, user.Fullname, user.Activated, user.Password.hash, user.ID, user.Version}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := m.DB.QueryRowContext(ctx, query, args...).Scan(&user.Version)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return ErrEditConflict
		case err.Error() == `pq: duplicate key value violates unique constraint "users_email_key"`:
			return ErrDuplicateEmail
		default:
			return err
		}
	}

	return nil

}

func (m *UserModel) Delete(id int64) error {
	query := `DELETE FROM users WHERE id = $1`

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := m.DB.ExecContext(ctx, query, id)
	return err
}

func (m *UserModel) GetForToken(tokenScope, tokenPlaintext string) (*User, error) {
	query := `
	SELECT u.id , u.fullname, u.email , u.password_hash, u.activated, u.created_at, u.updated_at, u.version
	FROM users AS u
	INNER JOIN tokens AS t ON u.id = t.user_id
	WHERE
		t.hash = $1 AND
		scope = $2 AND
		t.expiry > $3
	`

	hash := sha256.Sum256([]byte(tokenPlaintext))

	var user User

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	args := []any{hash[:], tokenScope, time.Now()}

	err := m.DB.QueryRowContext(ctx, query, args...).Scan(
		&user.ID,
		&user.Fullname,
		&user.Email,
		&user.Password.hash,
		&user.Activated,
		&user.CreatedAt,
		&user.UpdatedAt,
		&user.Version,
	)

	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil, ErrRecordNotFound
		default:
			return nil, err
		}
	}

	return &user, nil
}
