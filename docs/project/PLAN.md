# Plan

This plan covers the first release of `core-backend`, `core-frontend` and `app-template`. It collects the
parts that Go + React web apps tend to rebuild for every new project.

## Why

Teams that build several apps on the same stack usually start each one by copying the last: sign-in,
sessions, password hashing, secrets encryption, security headers, SPA serving, config loading and deploy
files. The copies drift. A helper's argument order changes in one app, hashing parameters differ in
another, and a security fix made in one app never reaches the others.

## Goals

1. A new app runs locally with sign-in, organizations and roles within 15 minutes of forking `app-template`.
2. Shared code lives in versioned libraries. A core fix reaches an app through `go get` or `npm update`.
3. Security defaults are on without configuration: strict CSP, CSRF, rate limits, Argon2id, scoped queries.
4. A developer who has not seen the code, or who builds mostly with a coding agent, can add a feature
   module by following one guide.
5. One deployable artifact per app: a single Go binary with the React app embedded, plus Postgres.

Every goal is measured against the six principles in the [README](../../README.md#principles): light,
scalable, clean code, modular, secure by default, no AI slop.

## Non-goals

- A general-purpose framework for apps outside this stack (Go + Postgres + React).
- Microservices. Apps are modular monoliths (see [ADR 0002](adr/0002-modular-monolith-single-binary.md)).
- A plugin system that loads code at runtime. Modules are compiled in.
- Supporting databases other than Postgres.
- Public marketing and SEO pages in v1. They will come from a separate Astro repository (`core-site`) later.
- Feature-specific code (billing, issue tracking, budgeting) in the core.

## Scope of version 1

### core-backend

| Package | Provides |
|---|---|
| `app` | Composition root: loads config, opens the database, runs migrations, mounts modules, serves, shuts down gracefully |
| `config` | Environment variables into typed structs, with validation and `APP_` prefix |
| `httpx` | Router on `net/http`, middleware chain, JSON decoding with limits, problem+json errors |
| `db` | `pgx` pool, transaction helper, per-module SQL migrations, test database helper |
| `auth` | Argon2id passwords, sessions, CSRF, TOTP, one-time tokens, rate limits |
| `rbac` | Caller identity, permissions, access in one organization; `Registry.Require(permission)` checks it per route |
| `secrets` | AES-256-GCM encryption under a master key (key rotation planned) |
| `jobs` | River queue in Postgres, worker lifecycle |
| `blob` | S3-compatible object storage, downloads after a permission check |
| `mail` | SMTP sender and a `Sender` interface for HTTP email providers |
| `audit` | Append-only audit events |
| `web` | Serves the embedded React build with correct caching and CSP nonces |
| `modules/accounts` | Sign-up, sign-in, sign-out, email verification, password reset, TOTP, sessions list |
| `modules/orgs` | Organizations, memberships, roles, invitations |
| `modules/apitokens` | Personal and organization API tokens with scopes and expiry |
| `modules/admin` | Instance settings and user management for operators |

### core-frontend

One React single-page app per product. Packages are published to the public npm registry under the
`@mahrus-rohisyam` scope and managed with npm workspaces.

| Package | Provides |
|---|---|
| `core-config` | Shared strict TypeScript settings |
| `core-ui` | Design tokens and accessible components on native HTML elements, plain CSS |
| `core-api` | Typed fetch client factory, problem+json errors, CSRF header |
| `core-auth` | Session provider, route guards, sign-in, sign-up, reset and two-factor screens |
| `core-shell` | App layout, navigation, organization switcher, account and organization settings pages |

### app-template

A runnable app that imports both cores, with one example module ("notes") that shows every layer:
migration, SQL queries, OpenAPI paths, handlers, a React page and end-to-end tests. It also ships the
Makefile, Docker Compose files, Dockerfile, CI pipeline and agent kit.

## Milestones

Each milestone ends with working code, tests, updated docs and a tagged pre-release.

| # | Milestone | Done when | Status |
|---|---|---|---|
| M0 | Repository setup | MIT license, CI (lint, test, govulncheck, Trivy, gitleaks, `npm audit`), agent kit, docs skeleton, CONTRIBUTING and SECURITY files in both repos | Done, except Trivy and gitleaks in CI |
| M1 | Backend foundation | `app`, `config`, `log`, `httpx`, `db`, `web` and health endpoints; an example binary serves a page and `/api/v1/health` with all security headers | Done; logging lives in `httpx`, health endpoints are `/healthz` and `/readyz` |
| M2 | Accounts | Sign-up, sign-in, sessions, CSRF, password reset, email verification, TOTP, rate limits; negative tests for each flow | Done |
| M3 | Organizations and access | Orgs, memberships, roles, invitations, API tokens, audit events; a cross-organization access test for every endpoint | Done |
| M4 | Frontend foundation | `core-config`, `core-ui`, `core-api`, `core-auth`, `core-shell` published as pre-releases; a component gallery page in the example app | Done, not yet published to npm; component gallery replaced by the example app |
| M5 | App template | `app-template` forks and runs with `make dev`; the "Build your first feature" guide is followed by someone who did not write it | Not started |
| M6 | Platform services | `jobs`, `blob`, `secrets`, `mail` templates, `admin` module, Google sign-in | Not started (`secrets` and `mail` have their basics) |
| M7 | First real app | One app built on the template; gaps found go back into the core | Not started |

After M7, candidates are: `core-site` (Astro packages for public pages), passkeys and notifications.

## Documentation plan

Documentation is written for a developer who knows Go or React but has never seen these repositories, and
for people who build mostly with a coding agent.

| Kind | Where | Example |
|---|---|---|
| Quick start | each README | Fork, run, sign in |
| Guides (task-oriented) | `docs/guides/` in each repo | Add a module, add an endpoint, add a page, add a permission |
| Concepts | `docs/architecture.md`, `docs/erd.md` | Module contract, request flow, tenancy |
| Reference | Go doc comments, TSDoc, OpenAPI | Every exported symbol has a one-line comment |
| Decisions | `docs/adr/` | Why stdlib routing, why sessions instead of JWT |
| Working with agents | `docs/building-with-ai.md` | The loop, red flags in a diff, prompts that work |
| Changes | `CHANGELOG.md` | Every release, with upgrade notes for breaking changes |

Rules: English only, short sentences, commands that can be copied and run, one task per guide. Every guide
is followed end to end before a release.

## Decisions made

| Question | Decision | Date |
|---|---|---|
| License | MIT, in every repository | 2026-11-08 |
| npm registry and scope | Public npm registry, scope `@mahrus-rohisyam`, packages named `core-*` | 2026-11-08 |
| Package manager for the frontend | npm (workspaces), not pnpm | 2026-11-08 |
| Multi-tenancy | Organizations always present; `APP_TENANCY=single` or `multi` (core-backend ADR 0004) | 2026-11-08 |
| Frontend scope for v1 | One React SPA. Astro moves to a separate repository later | 2026-11-08 |
| Where shared docs live | Each repository is published on its own and carries a copy of these project docs in `docs/project/`, kept identical across repositories | 2026-11-08 |

## Open questions

| Question | Needed by | Proposal |
|---|---|---|
| Redis | M6 | Not required. Add only if a measured need appears ([ADR 0003](adr/0003-postgres-only-datastore.md)) |
| Migrating existing apps to the core | after M7 | Only when an app needs a core fix; not a goal by itself |
