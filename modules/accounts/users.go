package accounts

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/mahrus-rohisyam/core-backend/db"
	"github.com/mahrus-rohisyam/core-backend/httpx"
)

// User is an account as other modules and the API see it. It never includes the password hash.
type User struct {
	ID              string    `json:"id"`
	Email           string    `json:"email"`
	Name            string    `json:"name"`
	EmailVerified   bool      `json:"email_verified"`
	IsInstanceAdmin bool      `json:"is_instance_admin"`
	TOTPEnabled     bool      `json:"totp_enabled"`
	CreatedAt       time.Time `json:"created_at"`
}

const userColumns = `u.id::text, u.email, u.name, u.email_verified_at IS NOT NULL, u.is_instance_admin,
	EXISTS (SELECT 1 FROM totp_factors f WHERE f.user_id = u.id AND f.confirmed_at IS NOT NULL), u.created_at`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.EmailVerified, &u.IsInstanceAdmin, &u.TOTPEnabled, &u.CreatedAt)
	return u, err
}

// GetUser returns one active or disabled user by ID. Other modules use it instead of reading
// the users table.
func GetUser(ctx context.Context, q db.DBTX, userID string) (User, error) {
	u, err := scanUser(q.QueryRow(ctx, "SELECT "+userColumns+" FROM users u WHERE u.id = $1", userID))
	if db.IsNotFound(err) {
		return User{}, httpx.ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("accounts: get user: %w", err)
	}
	return u, nil
}

// GetUsers returns the users with the given IDs, keyed by ID. Missing IDs are left out.
func GetUsers(ctx context.Context, q db.DBTX, ids []string) (map[string]User, error) {
	rows, err := q.Query(ctx, "SELECT "+userColumns+" FROM users u WHERE u.id = ANY($1::uuid[])", ids)
	if err != nil {
		return nil, fmt.Errorf("accounts: get users: %w", err)
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (User, error) { return scanUser(r) })
	if err != nil {
		return nil, fmt.Errorf("accounts: get users: %w", err)
	}
	out := make(map[string]User, len(list))
	for _, u := range list {
		out[u.ID] = u
	}
	return out, nil
}

// IsActive reports whether the user exists and is not disabled.
func IsActive(ctx context.Context, q db.DBTX, userID string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM users WHERE id = $1 AND status = 'active')", userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("accounts: is active: %w", err)
	}
	return ok, nil
}

// UserIDByEmail returns the ID of the user with this normalized email, or "" when there is none.
func UserIDByEmail(ctx context.Context, q db.DBTX, email string) (string, error) {
	var userID string
	err := q.QueryRow(ctx, "SELECT id::text FROM users WHERE lower(email) = $1", email).Scan(&userID)
	if db.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("accounts: user by email: %w", err)
	}
	return userID, nil
}

// NormalizeEmail trims and lowercases an address, and reports whether it is a plain valid address.
func NormalizeEmail(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) > 254 || !strings.Contains(s, "@") {
		return s, false
	}
	a, err := mail.ParseAddress(s)
	return s, err == nil && a.Address == s && a.Name == ""
}

func checkName(name string) (string, *httpx.FieldError) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n == 0 || n > 100 {
		return name, &httpx.FieldError{Field: "name", Message: "must be 1 to 100 characters"}
	}
	if strings.ContainsAny(name, "\r\n\t<>") {
		return name, &httpx.FieldError{Field: "name", Message: "must not contain line breaks or < >"}
	}
	return name, nil
}
