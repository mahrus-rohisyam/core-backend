// Package web serves the embedded React single-page app.
package web

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/mahrus-rohisyam/core-backend/httpx"
)

// Base is the path the SPA is served under. Vite builds it with `base: "/app/"`.
const Base = "/app/"

// SPA serves a Vite build from fsys (the folder that contains index.html) under Base.
//
// Existing files are served as-is; files under assets/ have hashed names and are cached for a
// year. Every other path returns index.html, so client-side routes survive a reload. index.html
// is never cached and gets the request's CSP nonce on its <style> tags and in a
// <meta name="csp-nonce"> tag for libraries that inject styles at run time.
func SPA(fsys fs.FS) (http.Handler, error) {
	index, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		return nil, err
	}
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rel := strings.TrimPrefix(path.Clean(r.URL.Path), strings.TrimSuffix(Base, "/"))
		rel = strings.TrimPrefix(rel, "/")
		if rel != "" && rel != "index.html" {
			if st, err := fs.Stat(fsys, rel); err == nil && !st.IsDir() {
				if strings.HasPrefix(rel, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "public, max-age=3600")
				}
				r2 := r.Clone(r.Context())
				r2.URL.Path = "/" + rel
				files.ServeHTTP(w, r2)
				return
			}
			if path.Ext(rel) != "" {
				http.NotFound(w, r) // a missing asset must not return HTML
				return
			}
		}
		nonce := httpx.CSPNonce(r.Context())
		page := bytes.ReplaceAll(index, []byte("<style"), []byte(`<style nonce="`+nonce+`"`))
		page = bytes.Replace(page, []byte("<head>"), []byte(`<head><meta name="csp-nonce" content="`+nonce+`">`), 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(page)
	}), nil
}

// Placeholder is a minimal index.html for apps and tests without a frontend build.
var Placeholder fs.FS = mustSub(placeholder, "placeholder")

//go:embed placeholder
var placeholder embed.FS

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
