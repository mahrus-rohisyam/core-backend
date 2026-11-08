# 0005. Keep an OpenAPI spec as the contract and test it against the router

- Status: Accepted
- Date: 2026-11-08

## Context

The frontend needs exact types for the API, and external users need API docs. When types are written by
hand on both sides, they drift from the API, and the mismatch shows up only at run time.

The first draft of this ADR generated Go server interfaces from per-module spec fragments. That needs
a code generator and a bundling tool before the first endpoint exists.

## Decision

- `api/openapi.json` (OpenAPI 3.1) describes every core endpoint. It is embedded as `api.Spec`, so apps
  can merge it with the description of their own routes.
- A test starts the app with all core modules and fails when a route is served but not documented, or
  documented but not served.
- The frontend generates its TypeScript types from the spec. Go handlers are written by hand.
- Errors use `application/problem+json` (RFC 9457) with stable `type` paths such as
  `/problems/validation`, and an `errors` array for field-level messages.
- Paths live under `/api/v1`. Lists that can grow without bound use cursor pagination
  (`after` or `before`, `limit`, and `next` in the response).

## Consequences

- A change to an endpoint changes the spec in the same commit; reviewers read the spec change first.
- The route test checks paths and methods, not request and response shapes. Shapes are covered by the
  integration tests and by the frontend's type check.

## Alternatives considered

- **Generate Go interfaces from the spec (oapi-codegen).** Stronger guarantees, but a generator
  dependency and generated code to review. Worth revisiting when the API is larger.
- **Code first (generate the spec from Go annotations).** Less JSON to write, but the spec becomes an
  afterthought and breaking changes slip through.
- **gRPC or Connect.** Strong typing and streaming, but the browser and third parties expect JSON over HTTP.
