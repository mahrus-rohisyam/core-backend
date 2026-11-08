package app

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mahrus-rohisyam/core-backend/auth"
	"github.com/mahrus-rohisyam/core-backend/config"
	"github.com/mahrus-rohisyam/core-backend/httpx"
	"github.com/mahrus-rohisyam/core-backend/id"
	"github.com/mahrus-rohisyam/core-backend/mail"
	"github.com/mahrus-rohisyam/core-backend/rbac"
	"github.com/mahrus-rohisyam/core-backend/secrets"
)

// Registry is what a module receives in Register: shared services, plus methods to mount
// routes and plug into authentication.
type Registry struct {
	AppName     string
	Config      *config.Config
	DB          *pgxpool.Pool
	Logger      *slog.Logger
	Mail        mail.Sender
	Secrets     *secrets.Box
	Limiter     *auth.Limiter
	Permissions []rbac.Permission // every permission declared by every module

	router         *httpx.Router
	authenticators []Authenticator
	resolver       rbac.Resolver
	tasks          []task
}

// Authenticator identifies the caller of a request. It returns (nil, nil) when the request
// carries no credentials it understands, and an error when it carries invalid ones.
type Authenticator interface {
	Authenticate(r *http.Request) (*rbac.Principal, error)
}

type task struct {
	name  string
	every time.Duration
	fn    func(context.Context) error
}

// Handle mounts a handler. Patterns follow net/http.ServeMux, e.g. "GET /api/v1/orgs/{org}/notes".
func (r *Registry) Handle(pattern string, h httpx.HandlerFunc, mws ...httpx.Middleware) {
	r.router.HandleFunc(pattern, h, mws...)
}

// AddAuthenticator adds a way to identify callers. They are tried in the order added.
func (r *Registry) AddAuthenticator(a Authenticator) { r.authenticators = append(r.authenticators, a) }

// SetResolver sets who decides what a caller may do in an organization (the orgs module).
func (r *Registry) SetResolver(res rbac.Resolver) { r.resolver = res }

// Every runs fn periodically while the app serves. Errors are logged.
func (r *Registry) Every(name string, every time.Duration, fn func(context.Context) error) {
	r.tasks = append(r.tasks, task{name, every, fn})
}

// ClientIP returns the caller's IP, honoring APP_TRUST_PROXY.
func (r *Registry) ClientIP(req *http.Request) string {
	return httpx.ClientIP(req, r.Config.TrustProxy)
}

// URL returns an absolute URL in the app for a path such as "/app/reset-password".
func (r *Registry) URL(path string, query url.Values) string {
	u := *r.Config.PublicURL
	u.Path = path
	u.RawQuery = query.Encode()
	return u.String()
}

// RequireUser rejects anonymous requests with 401.
func (r *Registry) RequireUser() httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if rbac.PrincipalFrom(req.Context()) == nil {
				httpx.WriteError(w, req, httpx.ErrUnauthorized)
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}

// RequireSession allows only browser sessions, not API tokens. Use it for account settings:
// a leaked token must not be able to change a password or create more tokens.
func (r *Registry) RequireSession() httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := rbac.PrincipalFrom(req.Context())
			if p == nil {
				httpx.WriteError(w, req, httpx.ErrUnauthorized)
				return
			}
			if p.IsToken() {
				httpx.WriteError(w, req, httpx.ErrForbidden.WithDetail("API tokens cannot be used here; sign in instead"))
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}

// Require checks that the caller may use perm in the organization named by the {org} path
// value, and puts their rbac.Access in the request context. An empty perm only requires
// membership. Callers outside the organization get 404, so organization IDs do not leak;
// members without the permission get 403.
func (r *Registry) Require(perm string) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			p := rbac.PrincipalFrom(req.Context())
			if p == nil {
				httpx.WriteError(w, req, httpx.ErrUnauthorized)
				return
			}
			orgID := req.PathValue("org")
			if !id.Valid(orgID) {
				httpx.WriteError(w, req, httpx.ErrNotFound)
				return
			}
			access, err := r.resolver.Resolve(req.Context(), p, orgID)
			if errors.Is(err, rbac.ErrNotMember) {
				httpx.WriteError(w, req, httpx.ErrNotFound)
				return
			}
			if err != nil {
				httpx.WriteError(w, req, err)
				return
			}
			if perm != "" && !access.Can(perm) {
				httpx.WriteError(w, req, httpx.ErrForbidden)
				return
			}
			next.ServeHTTP(w, req.WithContext(rbac.WithAccess(req.Context(), access)))
		})
	}
}

func (r *Registry) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		for _, a := range r.authenticators {
			p, err := a.Authenticate(req)
			if err != nil {
				httpx.WriteError(w, req, err)
				return
			}
			if p != nil {
				ctx := rbac.WithPrincipal(req.Context(), p)
				ctx = httpx.WithLogger(ctx, httpx.Logger(ctx).With(slog.String("user_id", p.UserID)))
				req = req.WithContext(ctx)
				break
			}
		}
		next.ServeHTTP(w, req)
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// checkOrigin rejects state-changing requests that a browser marks as coming from another site.
func (r *Registry) checkOrigin(next http.Handler) http.Handler {
	want := r.Config.PublicURL.Scheme + "://" + r.Config.PublicURL.Host
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !safeMethod(req.Method) {
			if o := req.Header.Get("Origin"); o != "" && o != want {
				httpx.WriteError(w, req, httpx.ErrForbidden.WithDetail("cross-origin request blocked"))
				return
			}
			if req.Header.Get("Sec-Fetch-Site") == "cross-site" {
				httpx.WriteError(w, req, httpx.ErrForbidden.WithDetail("cross-site request blocked"))
				return
			}
		}
		next.ServeHTTP(w, req)
	})
}

// checkCSRF requires the session's CSRF token in X-CSRF-Token on state-changing requests made
// with a session cookie. API tokens are sent explicitly, so they need no CSRF token.
func (r *Registry) checkCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if p := rbac.PrincipalFrom(req.Context()); p != nil && p.SessionID != "" && !safeMethod(req.Method) {
			if !auth.Equal(req.Header.Get("X-CSRF-Token"), p.CSRFToken) {
				httpx.WriteError(w, req, httpx.ErrCSRF)
				return
			}
		}
		next.ServeHTTP(w, req)
	})
}

func (r *Registry) runTasks(ctx context.Context) {
	for _, t := range r.tasks {
		go func() {
			tick := time.NewTicker(t.every)
			defer tick.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tick.C:
					if err := t.fn(ctx); err != nil && ctx.Err() == nil {
						r.Logger.Error("periodic task failed", slog.String("task", t.name), slog.String("error", err.Error()))
					}
				}
			}
		}()
	}
}
