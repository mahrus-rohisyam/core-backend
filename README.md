# core-backend

The Go foundation for web apps: HTTP server, Postgres access, sign-in with two-factor authentication,
organizations, roles, API tokens, an audit log, encrypted secrets and email. Your app adds its own
features as modules; everything else comes from here.

> **Status: `v0.1.0` in development.** Everything in [What you get](#what-you-get) works and is tested.
> Items marked *(planned)* have no code yet; the milestones are in [docs/project/PLAN.md](docs/project/PLAN.md).

## Contents

- [What you get](#what-you-get)
- [Quick start](#quick-start)
- [Using it in an app](#using-it-in-an-app)
- [Writing a module](#writing-a-module)
- [Configuration](#configuration)
- [API](#api)
- [Developing core-backend](#developing-core-backend)
- [Principles](#principles)
- [Documentation](#documentation)
- [Contributing, security and license](#contributing-security-and-license)

## What you get

| Area | Included |
|---|---|
| HTTP | Router on `net/http`, strict CSP with nonces, HSTS, COOP/CORP, request IDs, strict JSON decoding with size limits, problem+json errors, graceful shutdown |
| Accounts | Sign-up, sign-in, email verification, password reset, TOTP two-factor with recovery codes, session list and sign-out everywhere, rate limits |
| Organizations | Organizations, members, built-in and custom roles, invitations by email, single- or multi-tenant mode |
| Access | Permission check per route, 404 for other organizations' data, API tokens limited to their creator's current permissions, audit log |
| Data | Postgres via pgx, per-module SQL migrations, transaction helper, a fresh test database per test |
| Platform | AES-256-GCM encrypted secrets, email over SMTP (or logged in development), periodic tasks |
| Web | Serves the React single-page app from the same binary under `/app/` |

*(planned)*: background jobs, file storage on S3-compatible services, sign-in with Google, an instance
admin module, an `app-template` repository that wires the backend and frontend together.

Requirements: Go 1.26 or newer, Postgres 17 or newer. Everything else (hosting, reverse proxy, email
provider) is your choice; products named in these docs are suggestions, not requirements.

## Quick start

With Go, Docker and `make` installed:

```sh
git clone https://github.com/mahrus-rohisyam/core-backend.git
cd core-backend
make up                     # Postgres on 127.0.0.1:55532
cp .env.example .env        # then set APP_MASTER_KEY to the output of: openssl rand -base64 32
make run                    # the example "notes" app on http://localhost:8080
```

Open `http://localhost:8080/api/v1/auth/config`. Without a frontend build the app serves a placeholder
page at `/app/`. Emails (verification, password reset, invitations) are written to the log until SMTP is
configured.

## Using it in an app

```sh
go get github.com/mahrus-rohisyam/core-backend@latest
```

`main.go` lists the modules the app uses. Order matters: `accounts`, then `orgs`, then the rest.

```go
package main

import (
    "github.com/mahrus-rohisyam/core-backend/app"
    "github.com/mahrus-rohisyam/core-backend/modules/accounts"
    "github.com/mahrus-rohisyam/core-backend/modules/apitokens"
    "github.com/mahrus-rohisyam/core-backend/modules/orgs"

    "example.com/notes/notes"
    "example.com/notes/web"
)

func main() {
    acc := accounts.New()
    app.Run(app.Options{Name: "Notes", SPA: web.Dist},
        acc,
        orgs.New(acc), // places new users in an organization when they sign up
        apitokens.New(acc),
        notes.New(),   // your feature
    )
}
```

`web.Dist` is the frontend build embedded with `//go:embed` (an `fs.FS` whose root holds `index.html`).
Leave `SPA` empty to serve a placeholder page.

### Commands

The binary reads its command from the first argument:

| Command | Does |
|---|---|
| `app serve` (default) | Applies migrations, then serves HTTP and runs periodic tasks until SIGTERM |
| `app migrate` | Applies migrations and exits |
| `app check-config` | Validates configuration and prints it with secrets hidden |

The first account that signs up becomes the instance admin and the owner of the first organization.

### Upgrading

```sh
go get github.com/mahrus-rohisyam/core-backend@v0.2.0
go mod tidy
```

Read the [CHANGELOG](CHANGELOG.md) section for every version you skip. New core migrations run on the
next start.

## Writing a module

A module is one feature with its own tables, routes and permissions. [examples/notes](examples/notes)
is a complete one; copy it.

```
notes/
├── notes.go          implements app.Module
├── migrations/       0001_notes.sql, 0002_..., applied once, in order
└── notes_test.go     including a test that another organization cannot reach a note
```

```go
func (m *Module) Name() string { return "notes" }

func (m *Module) Migrations() fs.FS { sub, _ := fs.Sub(migrations, "migrations"); return sub }

func (m *Module) Permissions() []rbac.Permission {
    return []rbac.Permission{
        {Key: "notes.read", Description: "Read notes", Member: true}, // Member: the built-in member role has it
        {Key: "notes.write", Description: "Create, edit and delete notes"},
    }
}

func (m *Module) Register(r *app.Registry) error {
    m.reg = r
    r.Handle("GET /api/v1/orgs/{org}/notes", m.list, r.Require("notes.read"))
    r.Handle("POST /api/v1/orgs/{org}/notes", m.create, r.Require("notes.write"))
    return nil
}

func (m *Module) list(w http.ResponseWriter, r *http.Request) error {
    a := rbac.AccessFrom(r.Context()) // set by Require: the organization and the caller's permissions
    rows, err := m.reg.DB.Query(r.Context(), "SELECT ... FROM notes_notes WHERE org_id = $1", a.OrgID)
    ...
}
```

What the core does for every route:

- `r.Require(perm)` answers 401 to anonymous callers, **404** to anyone outside the organization in
  `{org}`, and 403 to members without the permission. Owners and admins have every permission.
- Handlers return errors. Return an `*httpx.Error` (`httpx.ErrNotFound`, `httpx.Invalid(...)`, ...) to
  show the client a message; any other error is logged and answered with a generic 500.
- `httpx.Decode` rejects unknown fields, wrong content types and bodies over 64 KB.
- Browser requests that change state must carry the CSRF token; API tokens skip it.

Rules for your queries: always filter by `org_id`, name tables `<module>_<thing>`, and read other modules'
data only through their exported Go functions (`accounts.GetUsers`, `orgs.IsMember`), never their tables.
Test with `apptest`:

```go
srv := apptest.New(t, apptest.Options{}, acc, orgs.New(acc), notes.New())
alice := srv.SignUp(t, "alice@example.com")
alice.Post(t, "/api/v1/orgs/"+orgID+"/notes", map[string]string{"title": "Hi"}).Expect(t, http.StatusCreated)
```

## Configuration

All configuration is environment variables with the `APP_` prefix. `app check-config` prints them all.

| Variable | Default | Purpose |
|---|---|---|
| `APP_PUBLIC_URL` | required | Where users reach the app. Links in emails; https turns on `Secure` cookies and HSTS |
| `APP_DATABASE_URL` | required | Postgres connection string |
| `APP_MASTER_KEY` | required | 32 random bytes in base64 (`openssl rand -base64 32`). Encrypts stored secrets such as TOTP keys. Back it up: losing it makes them unreadable |
| `APP_HTTP_ADDR` | `127.0.0.1:8080` | Listen address |
| `APP_TENANCY` | `single` | `single`: one organization, everyone joins it. `multi`: each sign-up gets its own organization and users can create more |
| `APP_SIGNUP` | `invite` | `invite`: after the first account, only invited people can sign up. `open`: anyone can |
| `APP_TRUST_PROXY` | `false` | `true` behind a reverse proxy, to take the client IP from `X-Forwarded-For`. Keep `false` if the app is reachable directly |
| `APP_SESSION_IDLE` / `APP_SESSION_LIFETIME` | `168h` / `720h` | Sign out after this much inactivity / this long after sign-in |
| `APP_SMTP_HOST`, `APP_SMTP_PORT`, `APP_SMTP_USER`, `APP_SMTP_PASSWORD`, `APP_MAIL_FROM` | not set | Email over SMTP (STARTTLS on 587, TLS on 465). Without a host, emails are logged |
| `APP_LOG_LEVEL` / `APP_LOG_FORMAT` | `info` / `json` | `debug`…`error` / `json` or `text` |

## API

Every endpoint is described in [api/openapi.json](api/openapi.json) (OpenAPI 3.1); a test fails when the
spec and the router disagree. In short:

| Area | Endpoints |
|---|---|
| Sign-in | `/api/v1/auth/{config,sign-up,sign-in,sign-out,verify-email,password-reset}` |
| Account | `/api/v1/session`, `/api/v1/account` (name, password, TOTP, sessions) |
| Organizations | `/api/v1/orgs`, `/api/v1/orgs/{org}/{members,roles,invitations,audit-events}`, `/api/v1/invitations/{lookup,accept}` |
| API tokens | `/api/v1/orgs/{org}/api-tokens`; send as `Authorization: Bearer cbt_...` |
| Health | `/healthz` (process up), `/readyz` (database reachable) |

Errors are `application/problem+json`:

```json
{"type": "/problems/validation", "title": "Some fields are invalid", "status": 422,
 "errors": [{"field": "password", "message": "must be at least 12 characters"}]}
```

## Developing core-backend

Prerequisites: Go 1.26+, Docker with Compose, `make`.

| Target | Does |
|---|---|
| `make up` / `make down` | Start or stop Postgres (in memory, `127.0.0.1:55532`) |
| `make test` | All tests with the race detector. Database tests fail, not skip, without Postgres |
| `make lint` | `gofmt`, `go vet`, hidden-Unicode check |
| `make vuln` | `govulncheck` |
| `make build` / `make run` | Build or run the example app |

Workflow:

1. Branch from `dev`: `git switch -c feature/<short-name> dev`.
2. Change code, add tests, add a line under `Unreleased` in [CHANGELOG.md](CHANGELOG.md).
3. Run `make lint test vuln`.
4. Open a pull request into `dev`. Releases go from `dev` to `master` and are tagged there.

Building with a coding agent? The repository includes `AGENTS.md`, `CLAUDE.md`, skills and review
subagents. Read [docs/building-with-ai.md](docs/building-with-ai.md) first.

## Principles

The six shared principles, and what each means in this repository:

| Principle | Here |
|---|---|
| **Light** | Standard library first (`net/http`, `log/slog`, `crypto/*`). Two direct dependencies, each recorded in [docs/dependencies.md](docs/dependencies.md). One binary (about 16 MB) plus Postgres |
| **Scalable** | No in-process state: sessions, rate limits and migration locks live in Postgres, so several copies can run behind a proxy. Cursor pagination; indexes start with `org_id` |
| **Clean code** | Plain SQL you can read, errors returned with context, no global state, no `init()` side effects, no reflection magic. `gofmt` and `go vet` pass with zero warnings |
| **Modular** | A module owns its tables, migrations, routes and permissions, and talks to other modules through their Go API. Adding or removing a module is one line in `main.go` |
| **Secure by default** | Strict CSP, CSRF and origin checks, rate limits, Argon2id, hashed tokens, sealed secrets and organization-scoped queries are on without configuration |
| **No AI slop** | Agents follow `AGENTS.md`, check dependencies before using them and review their own diffs with the `code-slop` and `secure-coding` skills |

The reasons behind the main choices are in [docs/adr/](docs/adr/).

## Documentation

| Document | Contents |
|---|---|
| [docs/architecture.md](docs/architecture.md) | Packages, module contract, request flow, tenancy and access, testing |
| [docs/erd.md](docs/erd.md) | Core tables, ownership and retention |
| [docs/adr/](docs/adr/) | Backend decisions and their reasons |
| [docs/dependencies.md](docs/dependencies.md) | Every direct dependency and why it is here |
| [docs/project/](docs/project/) | Plan, deployment topology and decisions shared by all core repositories |
| [docs/building-with-ai.md](docs/building-with-ai.md) | Working safely with coding agents |

## Contributing, security and license

- [CONTRIBUTING.md](CONTRIBUTING.md) explains branches, commits and review.
- Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md), not in public issues.
- Licensed under [MIT](LICENSE).
