# 0001. Ship the core as versioned libraries; start apps from a template

- Status: Proposed
- Date: 2026-11-08

## Context

When a team starts each new app by copying the previous one, every app ends up with its own sign-in,
sessions, password hashing, secrets encryption, SPA serving and config loading. The copies drift:
argument order, hashing parameters and error handling differ. Security fixes have to be found and
applied in every app by hand.

The goal is that a new app only contains its own features, and that core fixes reach existing apps.

## Decision

- `core-backend` is a Go module. `core-frontend` is a set of npm packages. Both are released with
  version tags. Apps depend on them; apps do not copy their source.
- `app-template` is a separate, runnable repository that imports both cores and contains only wiring,
  one example module, deployment files and the agent kit. A new app is a fork of it.
- Code moves into the core when a second app needs it, not before.

## Consequences

- A fix in the core reaches an app through `go get` or `npm install` of the new version and a
  redeploy.
- The core needs a stable public API, a changelog and upgrade notes ([ADR 0004](0004-versioning-and-releases.md)).
- Changing core behavior means a release, which is slower than editing a copy. Apps can override
  behavior through the extension points each package offers (options, hooks, their own modules).
- Contributors need to know which repository a change belongs in. The rule: generic → core,
  app-specific → app.

## Alternatives considered

- **Template only (fork and own everything).** Fastest to start, but every fork drifts, which is the
  current problem.
- **One monorepo with all apps.** Makes shared changes atomic, but couples unrelated apps' releases and
  access rights, and other people could not use the core without the apps.
- **Git submodules or copying with a sync script.** Hides drift instead of preventing it, and merges get
  hard once an app edits the copy.
