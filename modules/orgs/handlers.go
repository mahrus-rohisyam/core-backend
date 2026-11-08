package orgs

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/mahrus-rohisyam/core-backend/audit"
	"github.com/mahrus-rohisyam/core-backend/auth"
	"github.com/mahrus-rohisyam/core-backend/config"
	"github.com/mahrus-rohisyam/core-backend/db"
	"github.com/mahrus-rohisyam/core-backend/httpx"
	"github.com/mahrus-rohisyam/core-backend/id"
	"github.com/mahrus-rohisyam/core-backend/mail"
	"github.com/mahrus-rohisyam/core-backend/modules/accounts"
	"github.com/mahrus-rohisyam/core-backend/rbac"
)

var errLastOwner = httpx.Conflict("an organization needs at least one owner; make someone else an owner first")

func (m *Module) listPermissions(w http.ResponseWriter, r *http.Request) error {
	httpx.JSON(w, http.StatusOK, map[string]any{"items": m.reg.Permissions})
	return nil
}

// Org is an organization as the caller sees it.
type Org struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Role        string    `json:"role"`
	Permissions []string  `json:"permissions,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

func (m *Module) listOrgs(w http.ResponseWriter, r *http.Request) error {
	p := rbac.PrincipalFrom(r.Context())
	rows, err := m.reg.DB.Query(r.Context(), `
		SELECT o.id::text, o.name, ro.key, o.created_at
		FROM memberships ms JOIN organizations o ON o.id = ms.org_id JOIN roles ro ON ro.id = ms.role_id
		WHERE ms.user_id = $1 AND ($2 = '' OR o.id::text = $2)
		ORDER BY o.name, o.id`, p.UserID, p.TokenOrgID)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Org, error) {
		var o Org
		return o, row.Scan(&o.ID, &o.Name, &o.Role, &o.CreatedAt)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": list})
	return nil
}

func (m *Module) createOrg(w http.ResponseWriter, r *http.Request) error {
	if m.reg.Config.Tenancy != config.TenancyMulti {
		return httpx.ErrForbidden.WithDetail("this app has a single organization")
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	name, err := checkOrgName(in.Name)
	if err != nil {
		return err
	}
	ctx := r.Context()
	p := rbac.PrincipalFrom(ctx)
	if ok, err := m.reg.Limiter.Allow(ctx, "org-create:"+p.UserID, 10, 24*time.Hour); err != nil || !ok {
		if err != nil {
			return err
		}
		return httpx.ErrTooMany
	}
	var orgID string
	err = db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		orgID, err = createOrg(ctx, tx, name, p.UserID)
		return err
	})
	if err != nil {
		return err
	}
	return m.writeOrg(w, r, http.StatusCreated, orgID, &rbac.Access{Role: "owner", Permissions: m.allKeys()})
}

func (m *Module) allKeys() []string {
	keys := make([]string, len(m.reg.Permissions))
	for i, p := range m.reg.Permissions {
		keys[i] = p.Key
	}
	return keys
}

func (m *Module) getOrg(w http.ResponseWriter, r *http.Request) error {
	a := rbac.AccessFrom(r.Context())
	return m.writeOrg(w, r, http.StatusOK, a.OrgID, a)
}

func (m *Module) writeOrg(w http.ResponseWriter, r *http.Request, status int, orgID string, a *rbac.Access) error {
	o := Org{ID: orgID, Role: a.Role, Permissions: a.Permissions}
	if err := m.reg.DB.QueryRow(r.Context(), "SELECT name, created_at FROM organizations WHERE id = $1", orgID).Scan(&o.Name, &o.CreatedAt); err != nil {
		return err
	}
	if o.Permissions == nil {
		o.Permissions = []string{}
	}
	httpx.JSON(w, status, o)
	return nil
}

func (m *Module) updateOrg(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name string `json:"name"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	name, err := checkOrgName(in.Name)
	if err != nil {
		return err
	}
	ctx := r.Context()
	a := rbac.AccessFrom(ctx)
	uid, tid := actor(a)
	err = db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "UPDATE organizations SET name = $2, updated_at = now() WHERE id = $1", a.OrgID, name); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: a.OrgID, ActorUserID: uid, ActorTokenID: tid, Action: "org.renamed",
			TargetType: "org", TargetID: a.OrgID, IP: m.ip(r), Details: map[string]any{"name": name}})
	})
	if err != nil {
		return err
	}
	return m.writeOrg(w, r, http.StatusOK, a.OrgID, a)
}

// Member is one person in an organization.
type Member struct {
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	RoleID    string    `json:"role_id"`
	Role      string    `json:"role"`
	RoleName  string    `json:"role_name"`
	CreatedAt time.Time `json:"joined_at"`
}

// GET /api/v1/orgs/{org}/members?after=<user id>&limit=<n>, ordered by user ID.
func (m *Module) listMembers(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a := rbac.AccessFrom(ctx)
	after := r.URL.Query().Get("after")
	if after != "" && !id.Valid(after) {
		return httpx.BadRequest("after must be a user ID")
	}
	limit := pageLimit(r, 100, 500)
	rows, err := m.reg.DB.Query(ctx, `
		SELECT ms.user_id::text, ms.role_id::text, ro.key, ro.name, ms.created_at
		FROM memberships ms JOIN roles ro ON ro.id = ms.role_id
		WHERE ms.org_id = $1 AND ($2 = '' OR ms.user_id > $2::uuid)
		ORDER BY ms.user_id LIMIT $3`, a.OrgID, after, limit)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Member, error) {
		var mb Member
		return mb, row.Scan(&mb.UserID, &mb.RoleID, &mb.Role, &mb.RoleName, &mb.CreatedAt)
	})
	if err != nil {
		return err
	}
	ids := make([]string, len(list))
	for i, mb := range list {
		ids[i] = mb.UserID
	}
	users, err := accounts.GetUsers(ctx, m.reg.DB, ids)
	if err != nil {
		return err
	}
	for i := range list {
		u := users[list[i].UserID]
		list[i].Email, list[i].Name = u.Email, u.Name
	}
	next := ""
	if len(list) == limit {
		next = list[len(list)-1].UserID
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": list, "next": next})
	return nil
}

func pageLimit(r *http.Request, def, max int) int {
	n, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || n <= 0 {
		return def
	}
	return min(n, max)
}

type roleRow struct {
	ID          string   `json:"id"`
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	BuiltIn     bool     `json:"built_in"`
	Permissions []string `json:"permissions"`
}

// role loads a role usable in orgID: a built-in role or one of the organization's custom roles.
func (m *Module) role(ctx context.Context, q db.DBTX, orgID, roleID string) (roleRow, error) {
	if !id.Valid(roleID) {
		return roleRow{}, httpx.ErrNotFound
	}
	var ro roleRow
	err := q.QueryRow(ctx, "SELECT id::text, key, name FROM roles WHERE id = $1 AND (org_id IS NULL OR org_id = $2)", roleID, orgID).
		Scan(&ro.ID, &ro.Key, &ro.Name)
	if db.IsNotFound(err) {
		return roleRow{}, httpx.ErrNotFound
	}
	if err != nil {
		return roleRow{}, err
	}
	ro.BuiltIn = ro.Key != "custom"
	ro.Permissions, err = m.rolePermissions(ctx, q, ro.ID, ro.Key)
	if ro.Permissions == nil {
		ro.Permissions = []string{}
	}
	return ro, err
}

// mayGrant reports whether the caller may give someone the role, or change someone who has it:
// only owners handle owners, and nobody hands out permissions they do not have themselves.
func mayGrant(a *rbac.Access, ro roleRow) bool {
	if ro.Key == "owner" {
		return a.Role == "owner"
	}
	for _, p := range ro.Permissions {
		if !a.Can(p) {
			return false
		}
	}
	return true
}

// memberRole returns the role of userID in orgID, locking the membership row.
func (m *Module) memberRole(ctx context.Context, tx pgx.Tx, orgID, userID string) (roleRow, error) {
	if !id.Valid(userID) {
		return roleRow{}, httpx.ErrNotFound
	}
	var roleID string
	err := tx.QueryRow(ctx, "SELECT role_id::text FROM memberships WHERE org_id = $1 AND user_id = $2 FOR UPDATE", orgID, userID).Scan(&roleID)
	if db.IsNotFound(err) {
		return roleRow{}, httpx.ErrNotFound
	}
	if err != nil {
		return roleRow{}, err
	}
	return m.role(ctx, tx, orgID, roleID)
}

// ownerCount locks the organization row so that concurrent changes cannot remove the last owner.
func ownerCount(ctx context.Context, tx pgx.Tx, orgID string) (int, error) {
	if _, err := tx.Exec(ctx, "SELECT 1 FROM organizations WHERE id = $1 FOR UPDATE", orgID); err != nil {
		return 0, err
	}
	var n int
	err := tx.QueryRow(ctx, "SELECT count(*) FROM memberships WHERE org_id = $1 AND role_id = $2", orgID, RoleOwner).Scan(&n)
	return n, err
}

func (m *Module) updateMember(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		RoleID string `json:"role_id"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	a := rbac.AccessFrom(ctx)
	userID := r.PathValue("user")
	return db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		owners, err := ownerCount(ctx, tx, a.OrgID)
		if err != nil {
			return err
		}
		current, err := m.memberRole(ctx, tx, a.OrgID, userID)
		if err != nil {
			return err
		}
		next, err := m.role(ctx, tx, a.OrgID, in.RoleID)
		if httpx.IsStatus(err, http.StatusNotFound) {
			return httpx.Invalid(httpx.FieldError{Field: "role_id", Message: "is not a role in this organization"})
		}
		if err != nil {
			return err
		}
		if !mayGrant(a, current) || !mayGrant(a, next) {
			return httpx.ErrForbidden.WithDetail("you cannot grant or remove more access than you have")
		}
		if current.Key == "owner" && next.Key != "owner" && owners <= 1 {
			return errLastOwner
		}
		if _, err := tx.Exec(ctx, "UPDATE memberships SET role_id = $3 WHERE org_id = $1 AND user_id = $2", a.OrgID, userID, next.ID); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: a.OrgID, ActorUserID: a.Principal.UserID, Action: "member.role_changed",
			TargetType: "user", TargetID: userID, IP: m.ip(r), Details: map[string]any{"from": current.Name, "to": next.Name}}); err != nil {
			return err
		}
		httpx.NoContent(w)
		return nil
	})
}

// DELETE /api/v1/orgs/{org}/members/{user}: members.manage removes others; anyone may leave.
func (m *Module) removeMember(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a := rbac.AccessFrom(ctx)
	userID := r.PathValue("user")
	self := userID == a.Principal.UserID
	if !self && !a.Can(PermMembersManage) {
		return httpx.ErrForbidden
	}
	return db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		owners, err := ownerCount(ctx, tx, a.OrgID)
		if err != nil {
			return err
		}
		current, err := m.memberRole(ctx, tx, a.OrgID, userID)
		if err != nil {
			return err
		}
		if !self && !mayGrant(a, current) {
			return httpx.ErrForbidden.WithDetail("you cannot remove someone with more access than you have")
		}
		if current.Key == "owner" && owners <= 1 {
			return errLastOwner
		}
		if _, err := tx.Exec(ctx, "DELETE FROM memberships WHERE org_id = $1 AND user_id = $2", a.OrgID, userID); err != nil {
			return err
		}
		action := "member.removed"
		if self {
			action = "member.left"
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: a.OrgID, ActorUserID: a.Principal.UserID, Action: action,
			TargetType: "user", TargetID: userID, IP: m.ip(r)}); err != nil {
			return err
		}
		httpx.NoContent(w)
		return nil
	})
}

func (m *Module) listRoles(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a := rbac.AccessFrom(ctx)
	rows, err := m.reg.DB.Query(ctx, `SELECT id::text FROM roles WHERE org_id IS NULL OR org_id = $1
		ORDER BY org_id NULLS FIRST, id`, a.OrgID)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	list := make([]roleRow, 0, len(ids))
	for _, rid := range ids {
		ro, err := m.role(ctx, m.reg.DB, a.OrgID, rid)
		if err != nil {
			return err
		}
		list = append(list, ro)
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": list})
	return nil
}

type roleInput struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

func (m *Module) checkRole(a *rbac.Access, in roleInput) (string, []string, error) {
	var bad []httpx.FieldError
	name := strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(name); n == 0 || n > 50 || strings.ContainsAny(name, "\r\n\t<>") {
		bad = append(bad, httpx.FieldError{Field: "name", Message: "must be 1 to 50 characters, without line breaks or < >"})
	}
	keys := m.allKeys()
	var perms []string
	for _, p := range in.Permissions {
		switch {
		case !slices.Contains(keys, p):
			bad = append(bad, httpx.FieldError{Field: "permissions", Message: fmt.Sprintf("%q is not a permission of this app", p)})
		case !a.Can(p):
			bad = append(bad, httpx.FieldError{Field: "permissions", Message: fmt.Sprintf("you cannot grant %q because you do not have it", p)})
		case !slices.Contains(perms, p):
			perms = append(perms, p)
		}
	}
	if len(bad) > 0 {
		return "", nil, httpx.Invalid(bad...)
	}
	return name, perms, nil
}

func savePermissions(ctx context.Context, tx pgx.Tx, roleID string, perms []string) error {
	if _, err := tx.Exec(ctx, "DELETE FROM role_permissions WHERE role_id = $1", roleID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "INSERT INTO role_permissions (role_id, permission) SELECT $1, unnest($2::text[])", roleID, perms)
	return err
}

func (m *Module) createRole(w http.ResponseWriter, r *http.Request) error {
	var in roleInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	a := rbac.AccessFrom(ctx)
	name, perms, err := m.checkRole(a, in)
	if err != nil {
		return err
	}
	roleID := id.New()
	err = db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, "INSERT INTO roles (id, org_id, key, name) VALUES ($1, $2, 'custom', $3)", roleID, a.OrgID, name)
		if db.IsUniqueViolation(err, "roles_org_name") {
			return httpx.Invalid(httpx.FieldError{Field: "name", Message: "a role with this name already exists"})
		}
		if err != nil {
			return err
		}
		if err := savePermissions(ctx, tx, roleID, perms); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: a.OrgID, ActorUserID: a.Principal.UserID, Action: "role.created",
			TargetType: "role", TargetID: roleID, IP: m.ip(r), Details: map[string]any{"name": name, "permissions": perms}})
	})
	if err != nil {
		return err
	}
	ro, err := m.role(ctx, m.reg.DB, a.OrgID, roleID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusCreated, ro)
	return nil
}

// customRole loads one of the organization's custom roles for change; built-in roles are fixed.
func (m *Module) customRole(ctx context.Context, tx pgx.Tx, a *rbac.Access, roleID string) (roleRow, error) {
	ro, err := m.role(ctx, tx, a.OrgID, roleID)
	if err != nil {
		return ro, err
	}
	if ro.BuiltIn {
		return ro, httpx.ErrForbidden.WithDetail("built-in roles cannot be changed")
	}
	if !mayGrant(a, ro) {
		return ro, httpx.ErrForbidden.WithDetail("this role has permissions you do not have")
	}
	return ro, nil
}

func (m *Module) updateRole(w http.ResponseWriter, r *http.Request) error {
	var in roleInput
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	a := rbac.AccessFrom(ctx)
	name, perms, err := m.checkRole(a, in)
	if err != nil {
		return err
	}
	roleID := r.PathValue("role")
	err = db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		if _, err := m.customRole(ctx, tx, a, roleID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "UPDATE roles SET name = $2 WHERE id = $1", roleID, name)
		if db.IsUniqueViolation(err, "roles_org_name") {
			return httpx.Invalid(httpx.FieldError{Field: "name", Message: "a role with this name already exists"})
		}
		if err != nil {
			return err
		}
		if err := savePermissions(ctx, tx, roleID, perms); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: a.OrgID, ActorUserID: a.Principal.UserID, Action: "role.updated",
			TargetType: "role", TargetID: roleID, IP: m.ip(r), Details: map[string]any{"name": name, "permissions": perms}})
	})
	if err != nil {
		return err
	}
	ro, err := m.role(ctx, m.reg.DB, a.OrgID, roleID)
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, ro)
	return nil
}

func (m *Module) deleteRole(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a := rbac.AccessFrom(ctx)
	roleID := r.PathValue("role")
	return db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		ro, err := m.customRole(ctx, tx, a, roleID)
		if err != nil {
			return err
		}
		var used bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM memberships WHERE role_id = $1)", roleID).Scan(&used); err != nil {
			return err
		}
		if used {
			return httpx.Conflict("members still have this role; give them another role first")
		}
		if _, err := tx.Exec(ctx, "DELETE FROM roles WHERE id = $1", roleID); err != nil {
			return err
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: a.OrgID, ActorUserID: a.Principal.UserID, Action: "role.deleted",
			TargetType: "role", TargetID: roleID, IP: m.ip(r), Details: map[string]any{"name": ro.Name}}); err != nil {
			return err
		}
		httpx.NoContent(w)
		return nil
	})
}

// Invitation is a pending invitation. The token is only ever in the email.
type Invitation struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	RoleID    string    `json:"role_id"`
	RoleName  string    `json:"role_name"`
	InvitedBy string    `json:"invited_by"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

const invitationTTL = 7 * 24 * time.Hour

func (m *Module) listInvitations(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a := rbac.AccessFrom(ctx)
	rows, err := m.reg.DB.Query(ctx, `
		SELECT i.id::text, i.email, i.role_id::text, ro.name, coalesce(i.invited_by::text, ''), i.expires_at, i.created_at
		FROM invitations i JOIN roles ro ON ro.id = i.role_id
		WHERE i.org_id = $1 AND i.accepted_at IS NULL AND i.expires_at > now()
		ORDER BY i.created_at DESC LIMIT 500`, a.OrgID)
	if err != nil {
		return err
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Invitation, error) {
		var inv Invitation
		return inv, row.Scan(&inv.ID, &inv.Email, &inv.RoleID, &inv.RoleName, &inv.InvitedBy, &inv.ExpiresAt, &inv.CreatedAt)
	})
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": list})
	return nil
}

func (m *Module) createInvitation(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Email  string `json:"email"`
		RoleID string `json:"role_id"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	a := rbac.AccessFrom(ctx)
	email, ok := accounts.NormalizeEmail(in.Email)
	if !ok {
		return httpx.Invalid(httpx.FieldError{Field: "email", Message: "must be a valid email address"})
	}
	if ok, err := m.reg.Limiter.Allow(ctx, "invite:"+a.OrgID, 100, 24*time.Hour); err != nil || !ok {
		if err != nil {
			return err
		}
		return httpx.ErrTooMany
	}
	token, hash := auth.NewToken("")
	inv := Invitation{ID: id.New(), Email: email, InvitedBy: a.Principal.UserID}
	var orgName string
	err := db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		ro, err := m.role(ctx, tx, a.OrgID, in.RoleID)
		if httpx.IsStatus(err, http.StatusNotFound) {
			return httpx.Invalid(httpx.FieldError{Field: "role_id", Message: "is not a role in this organization"})
		}
		if err != nil {
			return err
		}
		if !mayGrant(a, ro) {
			return httpx.ErrForbidden.WithDetail("you cannot invite someone with more access than you have")
		}
		inv.RoleID, inv.RoleName = ro.ID, ro.Name
		existing, err := accounts.UserIDByEmail(ctx, tx, email)
		if err != nil {
			return err
		}
		member := false
		if existing != "" {
			if member, err = m.IsMember(ctx, tx, a.OrgID, existing); err != nil {
				return err
			}
		}
		if member {
			return httpx.Conflict("this person is already a member")
		}
		if err := tx.QueryRow(ctx, "SELECT name FROM organizations WHERE id = $1", a.OrgID).Scan(&orgName); err != nil {
			return err
		}
		// A new invitation for the same address replaces the old one.
		if _, err := tx.Exec(ctx, "DELETE FROM invitations WHERE org_id = $1 AND lower(email) = $2 AND accepted_at IS NULL", a.OrgID, email); err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `INSERT INTO invitations (id, org_id, email, role_id, token_hash, invited_by, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING expires_at, created_at`,
			inv.ID, a.OrgID, email, ro.ID, hash, a.Principal.UserID, time.Now().Add(invitationTTL)).Scan(&inv.ExpiresAt, &inv.CreatedAt)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{OrgID: a.OrgID, ActorUserID: a.Principal.UserID, Action: "invitation.created",
			TargetType: "invitation", TargetID: inv.ID, IP: m.ip(r), Details: map[string]any{"email": email, "role": ro.Name}})
	})
	if err != nil {
		return err
	}
	inviter, err := accounts.GetUser(ctx, m.reg.DB, a.Principal.UserID)
	if err != nil {
		return err
	}
	link := m.reg.URL("/app/accept-invitation", url.Values{"token": {token}})
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := m.reg.Mail.Send(sendCtx, mail.Message{
		To:      email,
		Subject: fmt.Sprintf("%s invited you to %s on %s", inviter.Name, orgName, m.reg.AppName),
		Body: fmt.Sprintf("%s (%s) invited you to join %s on %s.\n\nAccept the invitation:\n\n%s\n\nThe link expires in 7 days. If you did not expect this, ignore this email.",
			inviter.Name, inviter.Email, orgName, m.reg.AppName, link),
	}); err != nil {
		return fmt.Errorf("orgs: send invitation: %w", err)
	}
	httpx.JSON(w, http.StatusCreated, inv)
	return nil
}

func (m *Module) deleteInvitation(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	a := rbac.AccessFrom(ctx)
	invID := r.PathValue("id")
	if !id.Valid(invID) {
		return httpx.ErrNotFound
	}
	return db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, "DELETE FROM invitations WHERE id = $1 AND org_id = $2 AND accepted_at IS NULL", invID, a.OrgID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return httpx.ErrNotFound
		}
		if err := audit.Record(ctx, tx, audit.Event{OrgID: a.OrgID, ActorUserID: a.Principal.UserID, Action: "invitation.revoked",
			TargetType: "invitation", TargetID: invID, IP: m.ip(r)}); err != nil {
			return err
		}
		httpx.NoContent(w)
		return nil
	})
}

var errBadInvitation = httpx.NewError(http.StatusBadRequest, "invalid-invitation", "This invitation is invalid, expired or already used")

// POST /api/v1/invitations/lookup tells the accept page which organization invited whom.
func (m *Module) lookupInvitation(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Token string `json:"token"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	if ok, err := m.reg.Limiter.Allow(ctx, "invite-lookup:ip:"+m.ip(r), 30, time.Hour); err != nil || !ok {
		if err != nil {
			return err
		}
		return httpx.ErrTooMany
	}
	var out struct {
		OrgName string `json:"org_name"`
		Email   string `json:"email"`
	}
	err := m.reg.DB.QueryRow(ctx, `SELECT o.name, i.email FROM invitations i JOIN organizations o ON o.id = i.org_id
		WHERE i.token_hash = $1 AND i.accepted_at IS NULL AND i.expires_at > now()`, auth.HashToken(in.Token)).Scan(&out.OrgName, &out.Email)
	if db.IsNotFound(err) {
		return errBadInvitation
	}
	if err != nil {
		return err
	}
	httpx.JSON(w, http.StatusOK, out)
	return nil
}

func (m *Module) acceptInvitation(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Token string `json:"token"`
	}
	if err := httpx.Decode(r, &in); err != nil {
		return err
	}
	ctx := r.Context()
	p := rbac.PrincipalFrom(ctx)
	if ok, err := m.reg.Limiter.Allow(ctx, "invite-accept:"+p.UserID, 20, time.Hour); err != nil || !ok {
		if err != nil {
			return err
		}
		return httpx.ErrTooMany
	}
	u, err := accounts.GetUser(ctx, m.reg.DB, p.UserID)
	if err != nil {
		return err
	}
	var orgID string
	err = db.InTx(ctx, m.reg.DB, func(tx pgx.Tx) error {
		orgID, err = m.accept(ctx, tx, in.Token, u.ID, u.Email)
		return err
	})
	if err != nil {
		return err
	}
	a, err := m.Resolve(ctx, p, orgID)
	if err != nil {
		return err
	}
	return m.writeOrg(w, r, http.StatusOK, orgID, a)
}

// accept uses an invitation for userID. The invitation must be for the user's email address.
func (m *Module) accept(ctx context.Context, tx pgx.Tx, token, userID, email string) (string, error) {
	var invID, orgID, roleID, invEmail, inviter string
	err := tx.QueryRow(ctx, `SELECT id::text, org_id::text, role_id::text, email, coalesce(invited_by::text, '') FROM invitations
		WHERE token_hash = $1 AND accepted_at IS NULL AND expires_at > now() FOR UPDATE`, auth.HashToken(token)).
		Scan(&invID, &orgID, &roleID, &invEmail, &inviter)
	if db.IsNotFound(err) {
		return "", errBadInvitation
	}
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(invEmail, email) {
		return "", errBadInvitation.WithDetail("this invitation was sent to another email address")
	}
	// The inviter must still be allowed to grant the role, so that someone who was removed or
	// demoted cannot come back through an invitation they sent earlier.
	ro, err := m.role(ctx, tx, orgID, roleID)
	if err != nil {
		return "", err
	}
	if inviter == "" {
		return "", errBadInvitation.WithDetail("the person who sent this invitation no longer has an account")
	}
	access, err := m.Resolve(ctx, &rbac.Principal{UserID: inviter}, orgID)
	if errors.Is(err, rbac.ErrNotMember) || (err == nil && (!access.Can(PermMembersManage) || !mayGrant(access, ro))) {
		return "", errBadInvitation.WithDetail("the person who sent this invitation can no longer invite you with this role; ask for a new invitation")
	}
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, "UPDATE invitations SET accepted_at = now() WHERE id = $1", invID); err != nil {
		return "", err
	}
	if err := addMember(ctx, tx, orgID, userID, roleID); err != nil {
		return "", err
	}
	err = audit.Record(ctx, tx, audit.Event{OrgID: orgID, ActorUserID: userID, Action: "invitation.accepted", TargetType: "invitation", TargetID: invID})
	return orgID, err
}

// GET /api/v1/orgs/{org}/audit-events?before=<id>&limit=<n>, newest first.
func (m *Module) listAudit(w http.ResponseWriter, r *http.Request) error {
	a := rbac.AccessFrom(r.Context())
	var before int64
	if s := r.URL.Query().Get("before"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 0 {
			return httpx.BadRequest("before must be an event ID")
		}
		before = n
	}
	limit := pageLimit(r, 50, 200)
	list, err := audit.List(r.Context(), m.reg.DB, a.OrgID, before, limit)
	if err != nil {
		return err
	}
	var next int64
	if len(list) == limit {
		next = list[len(list)-1].ID
	}
	httpx.JSON(w, http.StatusOK, map[string]any{"items": list, "next": next})
	return nil
}
