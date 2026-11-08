# 0005. Ship the same agent kit and dependency policy in every repository

- Status: Proposed
- Date: 2026-11-08

## Context

Much of the code in these repositories is written with coding agents (Claude Code, Codex). Without
shared instructions, agents add dependencies from memory, write their own security code, and pad code
and docs with filler. AI-suggested package names that do not exist get registered by attackers, and
popular npm packages have been hijacked to publish malicious versions.

## Decision

- Every repository contains the same agent kit, kept identical across the core repositories:
  - `AGENTS.md` with a shared rules block, imported by `CLAUDE.md`.
  - Skills: `dependency-check`, `secure-coding`, `code-slop` and `no-ai-slop`.
  - Read-only review subagents: `security-reviewer`, `dependency-auditor` and `slop-reviewer`.
  - `docs/building-with-ai.md`, a guide for people who build mostly with coding agents.
  - A hook and script that reject hidden Unicode characters.
  - Permission prompts before any package install.
- No direct dependency is added without the `dependency-check` steps and a maintainer's approval,
  recorded in `docs/dependencies.md`.
- Versions are pinned exactly and must be at least 14 days old (`min-release-age=14` in `.npmrc`). npm
  install scripts and git-URL dependencies are disabled (`ignore-scripts`, `allow-git=none`).
- CI runs the same checks the kit asks for: govulncheck, Trivy, gitleaks, `npm audit` and the
  hidden-Unicode script on all tracked files.

## Consequences

- Agents and people follow the same rules, and CI catches what the instructions miss.
- Adding a dependency takes a few minutes of checking and an approval.
- The kit must be kept in step across repositories. Maintainers copy it with a script, so a change is
  one commit per repository.

## Alternatives considered

- **Instructions in each repository written separately.** They drift like the code did.
- **Rely on CI only.** Catches problems late, after the code is written around a bad dependency.
