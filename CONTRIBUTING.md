# Contributing

Thanks for helping. Small, focused pull requests are reviewed fastest.

## Before you start

- For a bug, open an issue with the steps to reproduce it, unless it is a security problem: then follow
  [SECURITY.md](SECURITY.md).
- For a new feature or a new dependency, open an issue first. The core stays small on purpose; many
  features belong in the app that needs them.

## Making a change

1. Fork, then branch from `dev`: `git switch -c feature/<short-name> dev`.
2. `make up` starts Postgres; `make test` runs every test.
3. Write the test first when you fix a bug. Anything access-controlled needs a negative test: another
   organization gets 404, a member without the permission gets 403.
4. A new or changed endpoint updates `api/openapi.json` in the same commit.
5. A new dependency needs a row in `docs/dependencies.md` and a maintainer's approval
   (see `.agents/skills/dependency-check/`).
6. Add a line under `Unreleased` in `CHANGELOG.md`.
7. Run `make lint test vuln` and open a pull request into `dev`.

## Style

- `gofmt` and `go vet` must pass with no output.
- Errors are wrapped with context (`fmt.Errorf("orgs: add member: %w", err)`) and returned.
- Comments explain why, not what. Exported names have a doc comment.
- Commit messages: imperative mood, under 72 characters on the first line, body when the reason is not
  obvious.

## Using coding agents

Agent-written code is welcome and held to the same bar. Read [docs/building-with-ai.md](docs/building-with-ai.md);
`AGENTS.md` holds the rules agents follow in this repository.
