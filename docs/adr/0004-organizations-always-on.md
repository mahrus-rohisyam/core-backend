# 0004. Every app has organizations, even single-tenant ones

- Status: Accepted
- Date: 2026-11-08

## Context

Some apps are used by one team (a self-hosted tool); others serve many customers (SaaS). Adding tenancy
to an app that started without it means adding `org_id` to every table and every query, then auditing all
of them. That is the most expensive retrofit a web app can need.

## Decision

- The `orgs` module is always installed. Every user works inside an organization; every org-owned row has
  `org_id`.
- An app chooses a mode with `APP_TENANCY`:
  - `single` (default for self-hosted tools): setup creates one organization, sign-ups join it, and the UI
    hides the organization switcher.
  - `multi`: users can create organizations and belong to several.
- Built-in roles are `owner`, `admin` and `member`. Apps add permissions; organizations may add custom roles.

## Consequences

- Moving from `single` to `multi` is a configuration change, not a migration.
- Single-tenant apps carry an `org_id` they rarely need. The cost is one column and one index per table.
- Every query and every test must handle the organization scope. The `secure-coding` skill and the
  cross-organization tests enforce it.

## Alternatives considered

- **Tenancy as an optional module.** Lighter for small apps, but apps that start without it pay the
  retrofit later, and core modules would need two code paths.
- **Database or schema per tenant.** Strong isolation, but migrations, connections and backups multiply
  with each customer.
