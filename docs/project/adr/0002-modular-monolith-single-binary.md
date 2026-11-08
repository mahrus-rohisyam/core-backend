# 0002. Build each app as a modular monolith shipped as one binary

- Status: Proposed
- Date: 2026-11-08

## Context

The target apps are built by small teams and often run on one server or a small container platform with
limited memory. A Go binary with the frontend embedded is simple to deploy, back up and roll back: one
artifact, one process, one version.

## Decision

- An app is one Go binary. It serves the React SPA (`/app/*`) and the API (`/api/*`), and runs
  background jobs in the same process by default. A public site, when an app needs one, is served at `/`.
- Features are **modules** compiled into the binary. A module owns its tables, migrations, routes,
  permissions and jobs, and talks to other modules only through their exported Go API.
- The same binary can run as `serve` (web and jobs), `web` (web only) or `worker` (jobs only), so jobs
  can move to a separate process when load requires it, without a code change.

## Consequences

- One artifact to build, scan, deploy and roll back. No network calls between services.
- Module boundaries are enforced by package visibility and review, not by the network. The
  `code-slop` and review rules must catch modules that read each other's tables.
- Scaling is vertical first, then by running more copies behind the reverse proxy. Sessions and jobs
  are in Postgres, so copies share state.

## Alternatives considered

- **Microservices.** Independent deploys, but far more operational work than these apps need.
- **Separate frontend hosting (Vercel, Node SSR).** Better for dynamic SEO pages, but adds a second
  runtime and cross-origin auth. A static build can still be put on a CDN later if needed.
