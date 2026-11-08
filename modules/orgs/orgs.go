// Package orgs is the core module for organizations: membership, built-in and custom roles,
// invitations, and the permission resolver every access check goes through.
package orgs

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
	"github.com/mahrus-rohisyam/core-backend/config"
	"github.com/mahrus-rohisyam/core-backend/db"
	"github.com/mahrus-rohisyam/core-backend/httpx"
	"github.com/mahrus-rohisyam/core-backend/id"
	"github.com/mahrus-rohisyam/core-backend/modules/accounts"
	"github.com/mahrus-rohisyam/core-backend/rbac"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Permissions declared by this module.
const (
	PermOrgManage     = "org.manage"
	PermMembersRead   = "members.read"
	PermMembersManage = "members.manage"
	PermRolesManage   = "roles.manage"
	PermAuditRead     = "audit.read"
)

// IDs of the built-in roles, seeded by the migration.
const (
	RoleOwner  = "00000000-0000-7000-8000-000000000001"
	RoleAdmin  = "00000000-0000-7000-8000-000000000002"
	RoleMember = "00000000-0000-7000-8000-000000000003"
)

// Module is the orgs module. Create it with New.
type Module struct {
	reg *app.Registry
}

// New returns the orgs module. It hooks into sign-up to place new users in an organization, so
// it needs the accounts module, which must be listed before it.
func New(acc *accounts.Module) *Module {
	m := &Module{}
	acc.OnSignUp(m.onSignUp)
	return m
}

// Name implements app.Module.
func (m *Module) Name() string { return "orgs" }

// Migrations implements app.Module.
func (m *Module) Migrations() fs.FS {
	sub, _ := fs.Sub(migrations, "migrations")
	return sub
}

// Permissions implements app.Module.
func (m *Module) Permissions() []rbac.Permission {
	return []rbac.Permission{
		{Key: PermOrgManage, Description: "Rename the organization"},
		{Key: PermMembersRead, Description: "See members and roles", Member: true},
		{Key: PermMembersManage, Description: "Invite, remove and change the role of members"},
		{Key: PermRolesManage, Description: "Create and edit custom roles"},
		{Key: PermAuditRead, Description: "Read the audit log"},
	}
}

// Register implements app.Module.
func (m *Module) Register(r *app.Registry) error {
	m.reg = r
	r.SetResolver(m)
	r.Every("orgs-purge", 24*time.Hour, m.purge)

	r.Handle("GET /api/v1/permissions", m.listPermissions, r.RequireUser())
	r.Handle("GET /api/v1/orgs", m.listOrgs, r.RequireUser())
	r.Handle("POST /api/v1/orgs", m.createOrg, r.RequireSession())
	r.Handle("GET /api/v1/orgs/{org}", m.getOrg, r.Require(""))
	r.Handle("PATCH /api/v1/orgs/{org}", m.updateOrg, r.Require(PermOrgManage))

	r.Handle("GET /api/v1/orgs/{org}/members", m.listMembers, r.Require(PermMembersRead))
	r.Handle("PATCH /api/v1/orgs/{org}/members/{user}", m.updateMember, r.RequireSession(), r.Require(PermMembersManage))
	r.Handle("DELETE /api/v1/orgs/{org}/members/{user}", m.removeMember, r.RequireSession(), r.Require(""))

	r.Handle("GET /api/v1/orgs/{org}/roles", m.listRoles, r.Require(PermMembersRead))
	r.Handle("POST /api/v1/orgs/{org}/roles", m.createRole, r.RequireSession(), r.Require(PermRolesManage))
	r.Handle("PATCH /api/v1/orgs/{org}/roles/{role}", m.updateRole, r.RequireSession(), r.Require(PermRolesManage))
	r.Handle("DELETE /api/v1/orgs/{org}/roles/{role}", m.deleteRole, r.RequireSession(), r.Require(PermRolesManage))

	r.Handle("GET /api/v1/orgs/{org}/invitations", m.listInvitations, r.Require(PermMembersManage))
	r.Handle("POST /api/v1/orgs/{org}/invitations", m.createInvitation, r.RequireSession(), r.Require(PermMembersManage))
	r.Handle("DELETE /api/v1/orgs/{org}/invitations/{id}", m.deleteInvitation, r.RequireSession(), r.Require(PermMembersManage))
	r.Handle("POST /api/v1/invitations/lookup", m.lookupInvitation)
	r.Handle("POST /api/v1/invitations/accept", m.acceptInvitation, r.RequireSession())

	r.Handle("GET /api/v1/orgs/{org}/audit-events", m.listAudit, r.Require(PermAuditRead))
	return nil
}

// Resolve implements rbac.Resolver. A token's permissions are cut down to what its creator may
// do now, so removing a member or changing their role also limits their tokens.
func (m *Module) Resolve(ctx context.Context, p *rbac.Principal, orgID string) (*rbac.Access, error) {
	if p.IsToken() && p.TokenOrgID != orgID {
		return nil, rbac.ErrNotMember
	}
	var roleID, key string
	err := m.reg.DB.QueryRow(ctx, `SELECT r.id::text, r.key FROM memberships ms JOIN roles r ON r.id = ms.role_id
		WHERE ms.org_id = $1 AND ms.user_id = $2`, orgID, p.UserID).Scan(&roleID, &key)
	if db.IsNotFound(err) {
		return nil, rbac.ErrNotMember
	}
	if err != nil {
		return nil, fmt.Errorf("orgs: resolve access: %w", err)
	}
	perms, err := m.rolePermissions(ctx, m.reg.DB, roleID, key)
	if err != nil {
		return nil, err
	}
	if p.IsToken() {
		perms = rbac.Intersect(p.TokenPermissions, perms)
	}
	return &rbac.Access{Principal: p, OrgID: orgID, Role: key, Permissions: perms}, nil
}

// rolePermissions returns the permissions a role grants, limited to those declared by modules.
func (m *Module) rolePermissions(ctx context.Context, q db.DBTX, roleID, key string) ([]string, error) {
	var out []string
	switch key {
	case "owner", "admin":
		for _, p := range m.reg.Permissions {
			out = append(out, p.Key)
		}
	case "member":
		for _, p := range m.reg.Permissions {
			if p.Member {
				out = append(out, p.Key)
			}
		}
	default:
		rows, err := q.Query(ctx, "SELECT permission FROM role_permissions WHERE role_id = $1", roleID)
		if err != nil {
			return nil, fmt.Errorf("orgs: role permissions: %w", err)
		}
		stored, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, fmt.Errorf("orgs: role permissions: %w", err)
		}
		for _, p := range m.reg.Permissions {
			if slices.Contains(stored, p.Key) {
				out = append(out, p.Key)
			}
		}
	}
	return out, nil
}

// IsMember reports whether userID belongs to orgID. Other modules use it, for example to check
// that an assignee is in the organization.
func (m *Module) IsMember(ctx context.Context, q db.DBTX, orgID, userID string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM memberships WHERE org_id = $1 AND user_id = $2)", orgID, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("orgs: is member: %w", err)
	}
	return ok, nil
}

// onSignUp places a new user: through their invitation, into the single organization, or into a
// new organization of their own.
func (m *Module) onSignUp(ctx context.Context, tx pgx.Tx, s accounts.SignUp) error {
	if s.Invitation != "" {
		_, err := m.accept(ctx, tx, s.Invitation, s.UserID, s.Email)
		return err
	}
	if m.reg.Config.Tenancy == config.TenancySingle {
		var orgID string
		err := tx.QueryRow(ctx, "SELECT id::text FROM organizations ORDER BY created_at LIMIT 1").Scan(&orgID)
		if db.IsNotFound(err) {
			_, err = createOrg(ctx, tx, m.reg.AppName, s.UserID)
			return err
		}
		if err != nil {
			return err
		}
		return addMember(ctx, tx, orgID, s.UserID, RoleMember)
	}
	_, err := createOrg(ctx, tx, s.Name+"'s organization", s.UserID)
	return err
}

func createOrg(ctx context.Context, tx pgx.Tx, name, ownerID string) (string, error) {
	orgID := id.New()
	if _, err := tx.Exec(ctx, "INSERT INTO organizations (id, name) VALUES ($1, $2)", orgID, name); err != nil {
		return "", fmt.Errorf("orgs: create: %w", err)
	}
	if err := addMember(ctx, tx, orgID, ownerID, RoleOwner); err != nil {
		return "", err
	}
	err := audit.Record(ctx, tx, audit.Event{OrgID: orgID, ActorUserID: ownerID, Action: "org.created", TargetType: "org", TargetID: orgID})
	return orgID, err
}

func addMember(ctx context.Context, q db.DBTX, orgID, userID, roleID string) error {
	_, err := q.Exec(ctx, `INSERT INTO memberships (org_id, user_id, role_id) VALUES ($1, $2, $3)
		ON CONFLICT (org_id, user_id) DO NOTHING`, orgID, userID, roleID)
	if err != nil {
		return fmt.Errorf("orgs: add member: %w", err)
	}
	return nil
}

func (m *Module) purge(ctx context.Context) error {
	_, err := m.reg.DB.Exec(ctx, "DELETE FROM invitations WHERE expires_at < now() - interval '7 days'")
	return err
}

func checkOrgName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n == 0 || n > 100 || strings.ContainsAny(name, "\r\n\t<>") {
		return name, httpx.Invalid(httpx.FieldError{Field: "name", Message: "must be 1 to 100 characters, without line breaks or < >"})
	}
	return name, nil
}

func (m *Module) ip(r *http.Request) string { return m.reg.ClientIP(r) }

func actor(a *rbac.Access) (userID, tokenID string) {
	return a.Principal.UserID, a.Principal.TokenID
}
