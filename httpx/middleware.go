package httpx

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware so that the first one listed runs first.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

type ctxKey int

const (
	requestIDKey ctxKey = iota
	loggerKey
	nonceKey
)

// RequestID returns the request's ID, or "" outside a request.
func RequestID(ctx context.Context) string {
	s, _ := ctx.Value(requestIDKey).(string)
	return s
}

// Logger returns the request-scoped logger, which carries the request ID.
func Logger(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// WithLogger returns ctx with l as the request logger. Authentication middleware uses it
// to add the user ID to every later log line.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// CSPNonce returns the per-request nonce allowed by the Content-Security-Policy.
func CSPNonce(ctx context.Context) string {
	s, _ := ctx.Value(nonceKey).(string)
	return s
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Base returns the middleware every app runs first: request ID, request logger, panic
// recovery and an access log line per request.
func Base(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rid := randomString(12)
			l := logger.With(slog.String("request_id", rid))
			ctx := context.WithValue(r.Context(), requestIDKey, rid)
			ctx = WithLogger(ctx, l)
			r = r.WithContext(ctx)
			w.Header().Set("X-Request-ID", rid)
			sw := &statusWriter{ResponseWriter: w}

			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler {
						panic(v)
					}
					Logger(r.Context()).Error("panic", slog.Any("panic", v), slog.String("stack", string(debug.Stack())))
					if !sw.wrote {
						WriteError(sw, r, NewError(http.StatusInternalServerError, "internal", "Something went wrong on our side").WithDetail("Request ID "+rid))
					}
				}
				l.Info("request",
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Int("status", sw.status()),
					slog.Duration("duration", time.Since(start)),
				)
			}()
			next.ServeHTTP(sw, r)
		})
	}
}

type statusWriter struct {
	http.ResponseWriter
	code  int
	wrote bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.code, w.wrote = code, true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.code, w.wrote = http.StatusOK, true
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) status() int {
	if w.code == 0 {
		return http.StatusOK
	}
	return w.code
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// SecurityHeaders sets the browser protections on every response. The CSP allows no inline
// scripts; inline styles need the per-request nonce from CSPNonce. hsts turns on
// Strict-Transport-Security and should be true only when the app is served over https.
func SecurityHeaders(hsts bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nonce := randomString(16)
			r = r.WithContext(context.WithValue(r.Context(), nonceKey, nonce))
			h := w.Header()
			h.Set("Content-Security-Policy", strings.Join([]string{
				"default-src 'self'",
				"script-src 'self'",
				"style-src 'self' 'nonce-" + nonce + "'",
				"img-src 'self' data: blob:",
				"font-src 'self'",
				"connect-src 'self'",
				"object-src 'none'",
				"base-uri 'self'",
				"form-action 'self'",
				"frame-ancestors 'none'",
			}, "; "))
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
			h.Set("X-Frame-Options", "DENY")
			if hsts {
				h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP returns the client's IP address. Behind a reverse proxy it uses the last address in
// X-Forwarded-For, which is the one the proxy added; earlier entries can be forged by the client.
// trustProxy must be false when the app is reachable without the proxy.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); ip != nil {
				return ip.String()
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
