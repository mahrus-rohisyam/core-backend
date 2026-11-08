package app_test

import (
	"context"
	"encoding/base64"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/mahrus-rohisyam/core-backend/app"
	"github.com/mahrus-rohisyam/core-backend/apptest"
	"github.com/mahrus-rohisyam/core-backend/config"
	"github.com/mahrus-rohisyam/core-backend/db/dbtest"
	"github.com/mahrus-rohisyam/core-backend/modules/accounts"
	"github.com/mahrus-rohisyam/core-backend/modules/orgs"
	"github.com/mahrus-rohisyam/core-backend/rbac"
)

func TestServesHealthAndSPA(t *testing.T) {
	acc := accounts.New()
	srv := apptest.New(t, apptest.Options{}, acc, orgs.New(acc))
	c := srv.Client()

	c.Get(t, "/healthz").Expect(t, http.StatusOK)
	c.Get(t, "/readyz").Expect(t, http.StatusOK)

	resp := c.Get(t, "/").Expect(t, http.StatusFound)
	if loc := resp.Header.Get("Location"); loc != "/app/" {
		t.Fatalf("redirect to %q", loc)
	}
	resp = c.Get(t, "/app/settings").Expect(t, http.StatusOK)
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "nonce-") {
		t.Fatal("no CSP on the app page")
	}
	resp = c.Get(t, "/api/v1/missing").Expect(t, http.StatusNotFound)
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/problem+json") {
		t.Fatalf("content type %q", resp.Header.Get("Content-Type"))
	}
}

type fakeModule struct {
	name  string
	perms []rbac.Permission
}

func (f fakeModule) Name() string                   { return f.name }
func (f fakeModule) Migrations() fs.FS              { return nil }
func (f fakeModule) Permissions() []rbac.Permission { return f.perms }
func (f fakeModule) Register(*app.Registry) error   { return nil }

func testConfig(t *testing.T) *config.Config {
	env := map[string]string{
		"APP_PUBLIC_URL":   "http://app.test",
		"APP_DATABASE_URL": dbtest.URL(t),
		"APP_MASTER_KEY":   base64.StdEncoding.EncodeToString(make([]byte, 32)),
	}
	cfg, err := config.LoadFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestNewRejectsBadSetups(t *testing.T) {
	cfg := testConfig(t)
	ctx := context.Background()
	cases := map[string][]app.Module{
		"no resolver":        {fakeModule{name: "notes"}},
		"duplicate name":     {fakeModule{name: "notes"}, fakeModule{name: "notes"}},
		"invalid permission": {fakeModule{name: "notes", perms: []rbac.Permission{{Key: "Notes"}}}},
		"duplicate permission": {
			fakeModule{name: "a", perms: []rbac.Permission{{Key: "x.read"}}},
			fakeModule{name: "b", perms: []rbac.Permission{{Key: "x.read"}}},
		},
	}
	for name, modules := range cases {
		if a, err := app.New(ctx, cfg, app.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, modules...); err == nil {
			a.Close()
			t.Errorf("%s: no error", name)
		}
	}
}
