// Package notes is an example feature module: it owns one table, declares two permissions and
// scopes every query to the organization in the URL.
package notes

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/mahrus-rohisyam/core-backend/app"
	"github.com/mahrus-rohisyam/core-backend/audit"
	"github.com/mahrus-rohisyam/core-backend/db"
	"github.com/mahrus-rohisyam/core-backend/httpx"
	"github.com/mahrus-rohisyam/core-backend/id"
	"github.com/mahrus-rohisyam/core-backend/rbac"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Module is the notes module.
type Module struct{ reg *app.Registry }

// New returns the notes module.
func New() *Module { return &Module{} }

func (m *Module) Name() string { return "notes" }

func (m *Module) Migrations() fs.FS {
	sub, _ := fs.Sub(migrations, "migrations")
	return sub
}

func (m *Module) Permissions() []rbac.Permission {
	return []rbac.Permission{
		{Key: "notes.read", Description: "Read notes", Member: true},
		{Key: "notes.write", Description: "Create, edit and delete notes", Member: true},
	}
}

func (m *Module) Register(r *app.Registry) error {
	m.reg = r
	r.Handle("GET /api/v1/orgs/{org}/notes", m.list, r.Require("notes.read"))
	r.Handle("POST /api/v1/orgs/{org}/notes", m.create, r.Require("notes.write"))
	r.Handle("GET /api/v1/orgs/{org}/notes/{id}", m.get, r.Require("notes.read"))
	r.Handle("DELETE /api/v1/orgs/{org}/notes/{id}", m.delete, r.Require("notes.write"))
	return nil
}

// Note is one note.
type Note struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	AuthorID  string    `json:"author_id"`
	CreatedAt time.Time `json:"created_at"`
}

const columns = "id::text, title, body, coalesce(author_id::text, ''), created_at"

func scan(row pgx.Row) (Note, error) {
	var n Note
	return n, row.Scan(&n.ID, &n.Title, &n.Body, &n.AuthorID, &n.CreatedAt)
}

func (m *Module) list(w http.ResponseWriter, r *http.Request) error {
	a := rbac.AccessFrom(r.Context())
	rows, err := m.reg.DB.Query(r.Context(), "SELECT "+columns+" FROM notes_notes WHERE org_id = $1 ORDER BY id DESC LIMIT 100", a.OrgID)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Note, error) { return scan(row) })
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": list})
	return nil
}

func (m *Module) create(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	in.Title = strings.TrimSpace(in.Title)
	if n := utf8.RuneCountInString(in.Title); n == 0 || n > 200 {
		return httpx.Invalid(httpx.FieldError{Field: "title", Message: "must be 1 to 200 characters"})
	}
	if len(in.Body) > 50_000 {
		return httpx.Invalid(httpx.FieldError{Field: "body", Message: "must be at most 50,000 bytes"})
	}
	ctx := r.Context()
	a := rbac.AccessFrom(ctx)
	var n Note
	err := db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		var err error
		n, err = scan(tx.QueryRow(ctx, `INSERT INTO notes_notes (id, org_id, author_id, title, body) VALUES ($1, $2, $3, $4, $5)
			RETURNING `+columns, id.New(), a.OrgID, a.Principal.UserID, in.Title, in.Body))
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: a.OrgID, ActorUserID: a.Principal.UserID, ActorTokenID: a.Principal.TokenID,
			Action: "note.created", TargetType: "note", TargetID: n.ID})
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, n)
	return nil
}

func (m *Module) get(w http.ResponseWriter, r *http.Request) error {
	a := rbac.AccessFrom(r.Context())
	noteID := r.PathValue("id")
	if !id.Valid(noteID) {
		return httpx.ErrNotFound
	}
	n, err := scan(m.reg.DB.QueryRow(r.Context(), "SELECT "+columns+" FROM notes_notes WHERE id = $1 AND org_id = $2", noteID, a.OrgID))
	if db.IsNotFound(err) {
		return httpx.ErrNotFound
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, n)
	return nil
}

func (m *Module) delete(w http.ResponseWriter, r *http.Request) error {
	a := rbac.AccessFrom(r.Context())
	noteID := r.PathValue("id")
	if !id.Valid(noteID) {
		return httpx.ErrNotFound
	}
	tag, err := m.reg.DB.Exec(r.Context(), "DELETE FROM notes_notes WHERE id = $1 AND org_id = $2", noteID, a.OrgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return httpx.ErrNotFound
	}
	httpx.NoContent(w)
	return nil
}
