# 0003. Use server-side sessions in a cookie, not JWTs

- Status: Accepted
- Date: 2026-11-08

## Context

The browser app and the API are served from the same origin ([project ADR 0002]). The two common
choices are JWTs and server-side sessions. Users need to sign out everywhere, admins need to disable
accounts immediately, and the session list must show active devices.

## Decision

- Browsers get a random 256-bit session token in a cookie: `HttpOnly`, `SameSite=Lax`, `Secure` when
  `APP_PUBLIC_URL` is https, path `/`. The database stores only its SHA-256 hash.
- Every state-changing request sends `X-CSRF-Token`: a random token stored with the session and returned by
  sign-in and `GET /api/v1/session`. Requests whose `Origin` or `Sec-Fetch-Site` shows another site are
  rejected as well.
- Sessions have an idle timeout (default 7 days) and an absolute lifetime (default 30 days). Sign-in
  always creates a new session; a password change signs out every other session, and a password reset
  signs out all of them.
- Scripts and integrations use API tokens (`Authorization: Bearer`), which are also stored hashed and can
  be narrowed to permissions and an expiry.

## Consequences

- Revocation is immediate: delete the row.
- Each authenticated request reads one indexed row. That is acceptable for these apps' load
  ([project ADR 0003]).
- No token is readable by JavaScript, so an XSS bug cannot steal sessions.

## Alternatives considered

- **JWT access and refresh tokens.** Stateless checks, but revocation needs a deny list (state again),
  and tokens are often stored where JavaScript can read them.
- **Third-party identity providers (Keycloak, Auth0, Clerk).** Strong features, but another service to
  run or pay for. Google sign-in is planned as an extra identity, not as the session system.

[project ADR 0002]: ../project/adr/0002-modular-monolith-single-binary.md
[project ADR 0003]: ../project/adr/0003-postgres-only-datastore.md
