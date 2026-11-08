# Changelog

All notable changes to this project. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and versions follow [Semantic Versioning](https://semver.org/). Before `v1.0.0`, a minor version may break
the public API; the entry then says how to upgrade.

## Unreleased

First version.

### Added

- `app`: module contract, `Registry`, `app.Run` with `serve`, `migrate` and `check-config`, graceful
  shutdown, periodic tasks, health endpoints, permission checks per route (`Require`).
- Platform packages: `config`, `httpx`, `db` (with migrations and `dbtest`), `auth`, `rbac`, `secrets`,
  `mail`, `audit`, `web`, `id`.
- `accounts` module: sign-up, sign-in, sign-out, email verification, password reset, password change,
  TOTP two-factor with recovery codes (setup needs the password), session list and revocation, rate
  limits, `OnPasswordReset` hook.
- `orgs` module: organizations in single- or multi-tenant mode, built-in and custom roles, members,
  invitations (checked against the sender's current rights when accepted), audit log endpoint, the
  permission resolver.
- `apitokens` module: organization-scoped API tokens limited to their creator's current permissions,
  revoked when the creator resets their password.
- `api/openapi.json` with a test that checks it against the router.
- `apptest` package for integration tests in apps.
- `examples/notes`: a small app with one feature module.
