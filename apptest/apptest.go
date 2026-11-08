// Package apptest runs a whole app against a fresh test database, for integration tests in the
// core and in apps:
//
//	srv := apptest.New(t, apptest.Options{}, accounts.New(), ...)
//	alice := srv.SignUp(t, "alice@example.com")
//	resp := alice.Get(t, "/api/v1/orgs")
package apptest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mahrus-rohisyam/core-backend/app"
	"github.com/mahrus-rohisyam/core-backend/config"
	"github.com/mahrus-rohisyam/core-backend/db/dbtest"
	"github.com/mahrus-rohisyam/core-backend/mail"
)

// Options adjust the test app.
type Options struct {
	// Env overrides configuration variables, e.g. {"APP_TENANCY": "multi"}.
	Env map[string]string
	// App options; Mail and Logger are set by apptest.
	App app.Options
}

// Server is a running test app.
type Server struct {
	*httptest.Server
	App  *app.App
	Mail *mail.Memory
}

// New starts the app with the modules on a fresh database. Everything is cleaned up when the
// test ends.
func New(t testing.TB, opts Options, modules ...app.Module) *Server {
	t.Helper()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	env := map[string]string{
		"APP_PUBLIC_URL":   "http://app.test",
		"APP_DATABASE_URL": dbtest.URL(t),
		"APP_MASTER_KEY":   base64.StdEncoding.EncodeToString(key),
		"APP_SIGNUP":       "open",
	}
	for k, v := range opts.Env {
		env[k] = v
	}
	cfg, err := config.LoadFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("apptest: config: %v", err)
	}
	mem := &mail.Memory{}
	o := opts.App
	o.Mail = mem
	o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := app.New(context.Background(), cfg, o, modules...)
	if err != nil {
		t.Fatalf("apptest: app: %v", err)
	}
	t.Cleanup(a.Close)
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(srv.Close)
	return &Server{Server: srv, App: a, Mail: mem}
}

// Client is a browser-like client: it keeps cookies and sends the CSRF token it was given.
type Client struct {
	srv    *Server
	http   *http.Client
	CSRF   string
	Bearer string // when set, sent as an API token instead of using cookies
}

// Client returns a new anonymous client.
func (s *Server) Client() *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{srv: s, http: &http.Client{
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// Token returns a client that authenticates with an API token only.
func (s *Server) Token(token string) *Client {
	c := s.Client()
	c.Bearer = token
	return c
}

// Password is the password SignUp uses.
const Password = "correct horse battery staple"

// SignUp creates an account and returns a client signed in to it.
func (s *Server) SignUp(t testing.TB, email string) *Client {
	t.Helper()
	return s.SignUpWith(t, map[string]any{"email": email, "name": strings.Split(email, "@")[0], "password": Password})
}

// SignUpWith signs up with a custom body (for example with an invitation token).
func (s *Server) SignUpWith(t testing.TB, body map[string]any) *Client {
	t.Helper()
	c := s.Client()
	resp := c.Post(t, "/api/v1/auth/sign-up", body)
	resp.Expect(t, http.StatusCreated)
	var out struct {
		CSRFToken string `json:"csrf_token"`
	}
	resp.Decode(t, &out)
	c.CSRF = out.CSRFToken
	return c
}

// Response is a finished request with its body read.
type Response struct {
	*http.Response
	Body []byte
}

// Expect fails the test unless the status matches.
func (r *Response) Expect(t testing.TB, status int) *Response {
	t.Helper()
	if r.StatusCode != status {
		t.Fatalf("%s %s: status %d, want %d; body: %s", r.Request.Method, r.Request.URL.Path, r.StatusCode, status, r.Body)
	}
	return r
}

// Decode unmarshals the JSON body into v.
func (r *Response) Decode(t testing.TB, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("%s %s: decode %s: %v", r.Request.Method, r.Request.URL.Path, r.Body, err)
	}
}

// Do sends a request with an optional JSON body.
func (c *Client) Do(t testing.TB, method, path string, body any, header http.Header) *Response {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("apptest: encode body: %v", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.srv.URL+path, rd)
	if err != nil {
		t.Fatalf("apptest: request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.CSRF != "" {
		req.Header.Set("X-CSRF-Token", c.CSRF)
	}
	if c.Bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.Bearer)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	client := c.http
	if c.Bearer != "" {
		client = &http.Client{CheckRedirect: c.http.CheckRedirect}
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("apptest: %s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("apptest: read body: %v", err)
	}
	return &Response{Response: resp, Body: b}
}

// Get sends a GET request.
func (c *Client) Get(t testing.TB, path string) *Response {
	return c.Do(t, http.MethodGet, path, nil, nil)
}

// Post sends a POST request with a JSON body.
func (c *Client) Post(t testing.TB, path string, body any) *Response {
	return c.Do(t, http.MethodPost, path, body, nil)
}

// Patch sends a PATCH request with a JSON body.
func (c *Client) Patch(t testing.TB, path string, body any) *Response {
	return c.Do(t, http.MethodPatch, path, body, nil)
}

// Delete sends a DELETE request.
func (c *Client) Delete(t testing.TB, path string) *Response {
	return c.Do(t, http.MethodDelete, path, nil, nil)
}

// LinkToken returns the "token" query parameter of the first link in the last email sent to addr.
func (s *Server) LinkToken(t testing.TB, addr string) string {
	t.Helper()
	// Some emails are sent in the background, so wait for a moment.
	msg, ok := s.Mail.Last(addr)
	for deadline := time.Now().Add(2 * time.Second); !ok && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
		msg, ok = s.Mail.Last(addr)
	}
	if !ok {
		t.Fatalf("apptest: no email to %s", addr)
	}
	for _, f := range strings.Fields(msg.Body) {
		if u, err := url.Parse(f); err == nil && u.Query().Get("token") != "" {
			return u.Query().Get("token")
		}
	}
	t.Fatalf("apptest: no link with a token in email to %s: %s", addr, msg.Body)
	return ""
}
