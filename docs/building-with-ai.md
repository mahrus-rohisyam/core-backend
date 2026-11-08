# Building with AI

A guide for people who build mostly by prompting a coding agent (Claude Code, Codex, Cursor and
similar), sometimes called vibe coding. You do not need to read every line the agent writes, but you
are still responsible for what gets merged. This repository is set up so that the agent follows the
same rules as an experienced developer, and so that the most common AI mistakes are caught before
they reach `dev`.

## What is already set up

When you open this repository in Claude Code or Codex, the agent reads `AGENTS.md` (Claude Code reads
it through `CLAUDE.md`). Without any extra prompting it will:

| Guardrail | What it prevents |
|---|---|
| `dependency-check` skill, plus a permission prompt before any install | Packages that do not exist, are typo-squatted, are brand new, abandoned or badly licensed |
| `secure-coding` skill | Missing permission checks, unscoped queries, unsafe redirects, secrets in code or logs |
| `code-slop` skill | Padded code: noise comments, needless abstractions, swallowed errors, invented APIs |
| `no-ai-slop` skill | Filler in docs, commit messages and UI text |
| Hidden-Unicode hook | Invisible characters that hide code or smuggle instructions to the agent |
| Review subagents (Claude Code) | A second, read-only pass for security, dependencies and slop |

## The loop

1. **Start from a branch.** `git switch -c feature/<what-you-are-building> dev`.
2. **Describe the outcome and how to check it.** Not "add notes", but:

   > Add a notes module. Members can create, edit and list notes in their organization. Only the author
   > or an admin can delete. Done when: the API has list/create/update/delete, the SPA has a notes page,
   > and tests show another organization cannot read or delete a note.

3. **Ask for a plan first.** "Plan this before writing code. List the files you will touch, the tables,
   the API paths, and any new dependency." Read the plan. This is the cheapest moment to say no.
4. **Let it build.** Answer its questions. If it asks to install a package, ask it to show the
   `dependency-check` card first.
5. **Ask for the reviews.** In Claude Code:

   > Run the security-reviewer, dependency-auditor and slop-reviewer subagents on this branch and fix
   > what they find.

   In other tools, ask it to apply the `secure-coding`, `dependency-check` and `code-slop` skills to its
   own diff.

6. **Run the checks yourself.** Backend: `make lint test security`. Frontend: `npm run lint`,
   `npm test`, `npm run build`. Then open the app and try the feature, including as a user who should
   not have access.
7. **Commit and push yourself.** The agent commits on your branch but never pushes or merges. Open a
   merge request into `dev`.

## Red flags in a diff

You can skim most of a diff. Stop and ask the agent to explain when you see any of these:

- A new line in `go.mod` or `package.json`, or a changed lockfile you did not expect.
- Changes in `auth`, `rbac`, sessions, CSRF, CORS or security headers.
- A query without `org_id`, or a new route without a permission check.
- Tests deleted, skipped (`t.Skip`, `it.skip`) or loosened to make them pass.
- Disabled checks: `// #nosec`, `eslint-disable`, `@ts-ignore`, `any`, `--no-verify`.
- `dangerouslySetInnerHTML`, `localStorage` with tokens, or secrets in `VITE_*` variables.
- A diff much larger than the request, or files changed that have nothing to do with it.

## Prompts that work

| Goal | Prompt |
|---|---|
| Understand before changing | "Explain how sign-in works in this repo, with file and line references. Don't change anything." |
| Keep changes small | "Do only step 1 of the plan, then stop and show me the diff." |
| Fix a bug properly | "Write a failing test that reproduces this bug, then fix it." |
| Check a dependency | "Run dependency-check on `<package>` and show me the card. Don't install it." |
| Remove slop | "Apply code-slop to your diff and fix what it finds. Only touch lines you changed." |
| Keep docs in step | "Update the README and the docs that describe what you changed, in plain English." |

## Limits

The guardrails lower the risk; they do not remove it. Agents can still misunderstand a requirement or
write a test that checks the wrong thing. Two habits cover most of what slips through: try the feature
yourself as a user without access, and do not merge a change you cannot explain in two sentences.
