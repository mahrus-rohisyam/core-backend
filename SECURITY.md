# Security policy

## Reporting a vulnerability

Do not open a public issue. Report it privately through GitHub:
**Security → Report a vulnerability** on this repository.

Include what an attacker can do, the steps or a proof of concept, and the affected version or commit.
You will get an answer within 7 days. Once a fix is released, the advisory credits you unless you
prefer otherwise.

## Supported versions

Before `v1.0.0`, only the latest minor version gets security fixes.

## Scope

In scope: the code in this repository, including the default configuration and the example app.

Out of scope: vulnerabilities in apps built on the core that come from the app's own code, and
issues that need a compromised server, database or `APP_MASTER_KEY`.

## What the core protects against

The defaults are listed in [docs/architecture.md](docs/architecture.md#security-defaults). Turning one off
in an app is the app's responsibility.
