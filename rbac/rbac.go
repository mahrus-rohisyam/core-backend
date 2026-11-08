// Package rbac describes who is calling (a Principal) and what they may do in an organization
// (their Access). Modules declare permissions; the orgs module decides who has them.
package rbac

import (
	"context"
	"errors"
	"slices"
	"strings"
)

// Permission is something a role can grant, such as "notes.write".
type Permission struct {
	Key         string `json:"key"`
	Description string `json:"description"`
	// Member grants the permission to the built-in member role. Owners and admins get every
	// permission.
	Member bool `json:"member"`
}

// Valid reports whether the key has the form "area.action" in lowercase.
func (p Permission) Valid() bool {
	area, action, ok := strings.Cut(p.Key, ".")
	return ok && area != "" && action != "" && p.Key == strings.ToLower(p.Key) && !strings.ContainsAny(p.Key, " *")
}

// Principal is the authenticated caller: a user with a browser session, or an API token.
type Principal struct {
	UserID    string
	SessionID string // set for browser sessions
	CSRFToken string // set for browser sessions

	TokenID          string   // set for API tokens
	TokenOrgID       string   // an API token works in this organization only
	TokenPermissions []string // and only with these permissions
}

// IsToken reports whether the caller authenticated with an API token.
func (p *Principal) IsToken() bool { return p.TokenID != "" }

type ctxKey int

const (
	principalKey ctxKey = iota
	accessKey
)

// WithPrincipal returns ctx carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// PrincipalFrom returns the caller, or nil when the request is anonymous.
func PrincipalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey).(*Principal)
	return p
}

// Access is what the caller may do in one organization.
type Access struct {
	Principal   *Principal
	OrgID       string
	Role        string // role key: owner, admin, member or a custom role
	Permissions []string
}

// Can reports whether the access includes perm.
func (a *Access) Can(perm string) bool { return slices.Contains(a.Permissions, perm) }

// WithAccess returns ctx carrying a.
func WithAccess(ctx context.Context, a *Access) context.Context {
	return context.WithValue(ctx, accessKey, a)
}

// AccessFrom returns the access set by the Require middleware. Handlers behind Require can rely
// on it being non-nil.
func AccessFrom(ctx context.Context) *Access {
	a, _ := ctx.Value(accessKey).(*Access)
	return a
}

// ErrNotMember means the caller has no access to the organization.
var ErrNotMember = errors.New("rbac: not a member")

// Resolver looks up a caller's access in an organization. It returns ErrNotMember when there is none.
type Resolver interface {
	Resolve(ctx context.Context, p *Principal, orgID string) (*Access, error)
}

// Intersect returns the permissions present in both lists. API tokens use it: a token can never do
// more than the person who created it can do now.
func Intersect(a, b []string) []string {
	var out []string
	for _, p := range a {
		if slices.Contains(b, p) {
			out = append(out, p)
		}
	}
	return out
}
