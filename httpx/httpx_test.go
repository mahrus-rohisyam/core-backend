package httpx

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func decode(body, contentType string) error {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	var dst struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	return Decode(r, &dst)
}

func TestDecode(t *testing.T) {
	cases := []struct {
		body, contentType string
		status            int
	}{
		{`{"name":"a"}`, "application/json", 0},
		{`{"name":"a"}`, "application/json; charset=utf-8", 0},
		{`{"name":"a"}`, "text/plain", http.StatusUnsupportedMediaType},
		{`{"name":"a"}`, "", http.StatusUnsupportedMediaType},
		{``, "application/json", http.StatusBadRequest},
		{`{"name":`, "application/json", http.StatusBadRequest},
		{`{"name":"a","admin":true}`, "application/json", http.StatusBadRequest},
		{`{"age":"x"}`, "application/json", http.StatusUnprocessableEntity},
		{`{"name":"a"}{"name":"b"}`, "application/json", http.StatusBadRequest},
		{`{"name":"` + strings.Repeat("a", DefaultBodyLimit) + `"}`, "application/json", http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		err := decode(c.body, c.contentType)
		if c.status == 0 {
			if err != nil {
				t.Errorf("%.40q: %v", c.body, err)
			}
			continue
		}
		if !IsStatus(err, c.status) {
			t.Errorf("%.40q (%s): got %v, want status %d", c.body, c.contentType, err, c.status)
		}
	}
}

func TestWriteErrorHidesInternalErrors(t *testing.T) {
	h := Chain(HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		return errors.New("pq: password authentication failed for user secret")
	}), Base(slog.New(slog.NewTextHandler(io.Discard, nil))))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Fatalf("content type %q", ct)
	}
	if rid := rec.Header().Get("X-Request-ID"); rid == "" || !strings.Contains(rec.Body.String(), rid) {
		t.Fatal("request ID missing from the error")
	}
}

func TestRecoverPanics(t *testing.T) {
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }),
		Base(slog.New(slog.NewTextHandler(io.Discard, nil))))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestRouter(t *testing.T) {
	rt := NewRouter()
	rt.HandleFunc("GET /api/v1/things/{id}", func(w http.ResponseWriter, r *http.Request) error {
		JSON(w, http.StatusOK, map[string]string{"id": r.PathValue("id")})
		return nil
	})
	rec := httptest.NewRecorder()
	rt.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/things/42", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"42"`) {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	rt.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/nothing", nil))
	if rec.Code != http.StatusNotFound || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/problem+json") {
		t.Fatalf("unknown API path: status %d, type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestSecurityHeaders(t *testing.T) {
	var nonce string
	h := SecurityHeaders(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { nonce = CSPNonce(r.Context()) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	csp := rec.Header().Get("Content-Security-Policy")
	if nonce == "" || !strings.Contains(csp, "'nonce-"+nonce+"'") || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("csp %q, nonce %q", csp, nonce)
	}
	for _, k := range []string{"Strict-Transport-Security", "X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if rec.Header().Get(k) == "" {
			t.Errorf("%s missing", k)
		}
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "10.0.0.1:5000"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.9")
	if got := ClientIP(r, false); got != "10.0.0.1" {
		t.Errorf("untrusted proxy: %s", got)
	}
	if got := ClientIP(r, true); got != "203.0.113.9" {
		t.Errorf("trusted proxy: %s", got)
	}
	r.Header.Set("X-Forwarded-For", "not-an-ip")
	if got := ClientIP(r, true); got != "10.0.0.1" {
		t.Errorf("bad header: %s", got)
	}
}
