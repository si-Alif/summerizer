package data

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"time"

	"github.com/si-Alif/summerizer/internal/validator"
)

const (
	ScopeActivation     = "activation"
	ScopePasswordReset  = "password-reset"
	ScopeAuthentication = "authentication"
)

type Token struct {
	Plaintext string `json:"token"`
	Hash      []byte `json:"-"`
	UserID    int64  `json:"-"`
	Expiry    time.Time  `json:"expiry"`
	Scope     string `json:"-"`
}

// generate Token instance for a specific user
func generateToken(userID int64, ttl time.Duration, scope string) *Token {
	token := &Token{
		Plaintext: rand.Text(),
		UserID:    userID,
		Expiry:    time.Now().Add(ttl),
		Scope:     scope,
	}

	hash := sha256.Sum256([]byte(token.Plaintext))
	token.Hash = hash[:]

	return token
}

func ValidateTokenPlaintext(v *validator.Validator, tokenPlaintext string) {
	v.Check(validator.NotBlank(tokenPlaintext), "token", "must be provided")
	v.Check(len(tokenPlaintext) == 26, "token", "must be 26 bytes long")
}

type TokenModel struct {
	DB *sql.DB
}

func (m TokenModel) New(userID int64, ttl time.Duration, scope string) (*Token, error) {
	token:= generateToken(userID, ttl, scope)

	err := m.Insert(token)

	return  token, err

}
func (m TokenModel) Insert(token *Token) error {
	query := `
		INSERT INTO tokens (hash, user_id, expiry, scope)
		VALUES ($1, $2, $3, $4)`

	args := []any{token.Hash, token.UserID, token.Expiry, token.Scope}

	ctx , cancel := context.WithTimeout(context.Background() , time.Second * 3)
	defer cancel()

	_ , err := m.DB.ExecContext(ctx , query , args...)

	return err
}

// delete all tokens for a specific user and for a specific scope (activation or password reset)
func (m TokenModel) DeleteAllTokenForUser(scope string, userID int64) error {
	query := `
		DELETE FROM tokens
		WHERE scope = $1 AND user_id = $2`

	args := []any{scope, userID}

	ctx , cancel := context.WithTimeout(context.Background() , time.Second * 3)
	defer cancel()

	_ , err := m.DB.ExecContext(ctx , query , args...)

	return err
}