// Package apitokens is the core module for API tokens. A token belongs to one person in one
// organization, and can never do more than that person can do there now.
package apitokens

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/mahrus-rohisyam/core-backend/app"
	"github.com/mahrus-rohisyam/core-backend/audit"
	"github.com/mahrus-rohisyam/core-backend/auth"
	"github.com/mahrus-rohisyam/core-backend/db"
	"github.com/mahrus-rohisyam/core-backend/httpx"
	"github.com/mahrus-rohisyam/core-backend/id"
	"github.com/mahrus-rohisyam/core-backend/modules/accounts"
	"github.com/mahrus-rohisyam/core-backend/rbac"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Prefix starts every token, so secret scanners can find leaked ones.
const Prefix = "cbt_"

// PermManage lets a member see and revoke everyone's tokens in the organization.
const PermManage = "tokens.manage"

// Module is the apitokens module. List it after accounts and orgs.
type Module struct {
	reg *app.Registry
}

// New returns the apitokens module. A password reset revokes the user's tokens.
func New(acc *accounts.Module) *Module {
	acc.OnPasswordReset(func(ctx context.Context, tx pgx.Tx, userID string) error {
		_, err := tx.Exec(ctx, "UPDATE api_tokens SET revoked_at = now() WHERE created_by = $1 AND revoked_at IS NULL", userID)
		return err
	})
	return &Module{}
}

// Name implements app.Module.
func (m *Module) Name() string { return "apitokens" }

// Migrations implements app.Module.
func (m *Module) Migrations() fs.FS {
	sub, _ := fs.Sub(migrations, "migrations")
	return sub
}

// Permissions implements app.Module.
func (m *Module) Permissions() []rbac.Permission {
	return []rbac.Permission{{Key: PermManage, Description: "See and revoke every API token in the organization"}}
}

// Register implements app.Module.
func (m *Module) Register(r *app.Registry) error {
	m.reg = r
	r.AddAuthenticator(m)
	r.Handle("GET /api/v1/orgs/{org}/api-tokens", m.list, r.RequireSession(), r.Require(""))
	r.Handle("POST /api/v1/orgs/{org}/api-tokens", m.create, r.RequireSession(), r.Require(""))
	r.Handle("DELETE /api/v1/orgs/{org}/api-tokens/{id}", m.revoke, r.RequireSession(), r.Require(""))
	return nil
}

var errBadToken = httpx.ErrUnauthorized.WithDetail("the API token is invalid, expired or revoked")

// Authenticate implements app.Authenticator for "Authorization: Bearer cbt_...".
func (m *Module) Authenticate(r *http.Request) (*rbac.Principal, error) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || !strings.HasPrefix(token, Prefix) {
		return nil, nil
	}
	if len(token) > 100 {
		return nil, errBadToken
	}
	ctx := r.Context()
	p := rbac.Principal{}
	var lastUsed *time.Time
	err := m.reg.DB.QueryRow(ctx, `SELECT id::text, org_id::text, created_by::text, permissions, last_used_at FROM api_tokens
		WHERE token_hash = $1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now())`, auth.HashToken(token)).
		Scan(&p.TokenID, &p.TokenOrgID, &p.UserID, &p.TokenPermissions, &lastUsed)
	if db.IsNotFound(err) {
		return nil, errBadToken
	}
	if err != nil {
		return nil, fmt.Errorf("apitokens: load: %w", err)
	}
	active, err := accounts.IsActive(ctx, m.reg.DB, p.UserID)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, errBadToken
	}
	if lastUsed == nil || time.Since(*lastUsed) > time.Minute {
		_, _ = m.reg.DB.Exec(ctx, "UPDATE api_tokens SET last_used_at = now() WHERE id = $1", p.TokenID)
	}
	return &p, nil
}

// Token is a token's metadata. The secret is only returned once, when it is created.
type Token struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Prefix      string     `json:"prefix"`
	CreatedBy   string     `json:"created_by"`
	Permissions []string   `json:"permissions"`
	ExpiresAt   *time.Time `json:"expires_at"`
	LastUsedAt  *time.Time `json:"last_used_at"`
	CreatedAt   time.Time  `json:"created_at"`
	Token       string     `json:"token,omitempty"`
}

func (m *Module) list(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a := rbac.AccessFrom(ctx)
	owner := a.Principal.UserID
	if a.Can(PermManage) {
		owner = ""
	}
	rows, err := m.reg.DB.Query(ctx, `SELECT id::text, name, prefix, created_by::text, permissions, expires_at, last_used_at, created_at
		FROM api_tokens WHERE org_id = $1 AND revoked_at IS NULL AND ($2 = '' OR created_by::text = $2)
		ORDER BY created_at DESC LIMIT 500`, a.OrgID, owner)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Token, error) {
		var t Token
		return t, row.Scan(&t.ID, &t.Name, &t.Prefix, &t.CreatedBy, &t.Permissions, &t.ExpiresAt, &t.LastUsedAt, &t.CreatedAt)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": list})
	return nil
}

func (m *Module) create(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name          string   `json:"name"`
		Permissions   []string `json:"permissions"`
		ExpiresInDays int      `json:"expires_in_days"` // 0 means the token does not expire
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	a := rbac.AccessFrom(ctx)
	var bad []httpx.FieldError
	name := strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(name); n == 0 || n > 100 || strings.ContainsAny(name, "\r\n\t<>") {
		bad = append(bad, httpx.FieldError{Field: "name", Message: "must be 1 to 100 characters, without line breaks or < >"})
	}
	perms := []string{}
	for _, p := range in.Permissions {
		if !a.Can(p) {
			bad = append(bad, httpx.FieldError{Field: "permissions", Message: fmt.Sprintf("you do not have %q in this organization", p)})
		} else if !slices.Contains(perms, p) {
			perms = append(perms, p)
		}
	}
	if len(perms) == 0 && len(bad) == 0 {
		bad = append(bad, httpx.FieldError{Field: "permissions", Message: "choose at least one permission"})
	}
	if in.ExpiresInDays < 0 || in.ExpiresInDays > 3650 {
		bad = append(bad, httpx.FieldError{Field: "expires_in_days", Message: "must be between 0 (never) and 3650"})
	}
	if len(bad) > 0 {
		return httpx.Invalid(bad...)
	}
	secret, hash := auth.NewToken(Prefix)
	t := Token{ID: id.New(), Name: name, Prefix: secret[:len(Prefix)+6], CreatedBy: a.Principal.UserID, Permissions: perms, Token: secret}
	if in.ExpiresInDays > 0 {
		exp := time.Now().Add(time.Duration(in.ExpiresInDays) * 24 * time.Hour)
		t.ExpiresAt = &exp
	}
	err := db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO api_tokens (id, org_id, created_by, name, prefix, token_hash, permissions, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING created_at`,
			t.ID, a.OrgID, t.CreatedBy, t.Name, t.Prefix, hash, t.Permissions, t.ExpiresAt).Scan(&t.CreatedAt)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: a.OrgID, ActorUserID: t.CreatedBy, Action: "api_token.created",
			TargetType: "api_token", TargetID: t.ID, IP: m.reg.ClientIP(r), Details: map[string]any{"name": name, "permissions": perms}})
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, t)
	return nil
}

func (m *Module) revoke(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a := rbac.AccessFrom(ctx)
	tokenID := r.PathValue("id")
	if !id.Valid(tokenID) {
		return httpx.ErrNotFound
	}
	owner := a.Principal.UserID
	if a.Can(PermManage) {
		owner = ""
	}
	return db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE api_tokens SET revoked_at = now()
			WHERE id = $1 AND org_id = $2 AND revoked_at IS NULL AND ($3 = '' OR created_by::text = $3)`, tokenID, a.OrgID, owner)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: a.OrgID, ActorUserID: a.Principal.UserID, Action: "api_token.revoked",
			TargetType: "api_token", TargetID: tokenID, IP: m.reg.ClientIP(r)}); err != nil {
			return err
		}
		httpx.NoContent(w)
		return nil
	})
}
