# 0001. Use the standard library HTTP server and router

- Status: Accepted
- Date: 2026-11-08

## Context

Go web apps often add a router library such as chi, gin or echo. Since Go 1.22, `ServeMux` matches
methods and path wildcards (`GET /api/v1/orgs/{org}/notes/{id}`), which covers what most apps use those
libraries for.

## Decision

Use `net/http` and `ServeMux` for routing. `httpx` adds a small middleware chain, strict JSON decoding
and problem+json errors on top. Handlers are plain `http.Handler`s.

## Consequences

- No router dependency to vet, upgrade or patch.
- Any `net/http` middleware or library works without adapters.
- No route groups. `httpx` provides a helper to mount a module's routes under a prefix with its middleware.

## Alternatives considered

- **chi.** Route groups and middleware are convenient, but `ServeMux` now covers the routing, and `httpx`
  covers the rest in a few dozen lines.
- **gin, echo, fiber.** Own context types, which spread through every handler and make leaving them
  expensive. Fiber is not `net/http`-compatible.
