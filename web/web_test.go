package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mahrus-rohisyam/core-backend/httpx"
)

func TestSPA(t *testing.T) {
	spa, err := SPA(fstest.MapFS{
		"index.html":      {Data: []byte("<html><head><style>a{}</style></head><body></body></html>")},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
		"favicon.svg":     {Data: []byte("<svg/>")},
	})
	if err != nil {
		t.Fatal(err)
	}
	h := httpx.SecurityHeaders(false)(spa)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	rec := get("/app/projects/42")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("client route: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	if !strings.Contains(body, `<style nonce="`) || !strings.Contains(body, `<meta name="csp-nonce"`) {
		t.Fatalf("nonce not injected: %s", body)
	}
	if rec := get("/app/assets/app-1.js"); !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset cache: %q", rec.Header().Get("Cache-Control"))
	}
	if rec := get("/app/favicon.svg"); rec.Header().Get("Cache-Control") != "public, max-age=3600" {
		t.Fatalf("file cache: %q", rec.Header().Get("Cache-Control"))
	}
	if rec := get("/app/assets/missing.js"); rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset: %d", rec.Code)
	}
	if rec := get("/app/../../etc/passwd"); strings.Contains(rec.Body.String(), "root:") {
		t.Fatal("path traversal")
	}
}

func TestPlaceholder(t *testing.T) {
	if _, err := SPA(Placeholder); err != nil {
		t.Fatal(err)
	}
}
