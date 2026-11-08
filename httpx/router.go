package httpx

import (
	"net/http"
	"sort"
)

// HandlerFunc is an http handler that returns an error. A returned error is written with
// WriteError, so handlers can `return err` instead of writing error responses themselves.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// ServeHTTP implements http.Handler.
func (f HandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := f(w, r); err != nil {
		WriteError(w, r, err)
	}
}

// Router is http.ServeMux plus per-route middleware and a list of registered routes.
type Router struct {
	mux    *http.ServeMux
	routes []string
}

// NewRouter returns an empty router.
func NewRouter() *Router { return &Router{mux: http.NewServeMux()} }

// Handle registers h for a ServeMux pattern such as "POST /api/v1/orgs/{org}/notes".
// Middleware listed here runs after the app-wide middleware, first one first.
func (rt *Router) Handle(pattern string, h http.Handler, mws ...Middleware) {
	rt.mux.Handle(pattern, Chain(h, mws...))
	rt.routes = append(rt.routes, pattern)
}

// HandleFunc is Handle for a HandlerFunc.
func (rt *Router) HandleFunc(pattern string, h HandlerFunc, mws ...Middleware) {
	rt.Handle(pattern, h, mws...)
}

// Routes returns the registered patterns, sorted.
func (rt *Router) Routes() []string {
	out := append([]string(nil), rt.routes...)
	sort.Strings(out)
	return out
}

// ServeHTTP implements http.Handler. Unknown /api paths get a problem+json 404.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// mux.Handler only looks the route up; mux.ServeHTTP must still run so that
	// r.PathValue is filled in.
	if _, pattern := rt.mux.Handler(r); pattern == "" && len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
		WriteError(w, r, ErrNotFound)
		return
	}
	rt.mux.ServeHTTP(w, r)
}
