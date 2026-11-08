---
name: code-slop
description: Find and remove AI-slop patterns in Go and TypeScript code before a change is reported as done. Use on every finished code change, when reviewing AI-written code, or when the user asks whether code looks generated. Covers noise comments, speculative abstractions, defensive code for impossible states, swallowed errors, invented APIs, duplicated core functionality and padded tests. For prose (docs, commit messages, UI copy), use no-ai-slop instead.
---

# Code slop

Slop is code that looks complete but is longer, vaguer or less correct than it needs to be. Review
the diff, not the whole repo. Each finding needs a file, a line and a concrete fix.

## Patterns

**Comments**
- Comments that restate the code (`// increment counter`, `// Helper function to get user`).
- Change-log comments (`// Updated to fix bug`, `// New implementation`, `// Added for X`).
- Section banners, emoji, and "Note:" / "Important:" prefixes.
- Commented-out code, and TODOs without an issue reference.
- Keep comments that say *why*: a constraint, a non-obvious invariant, a link to an incident.

**Structure**
- An interface with one implementation, defined next to that implementation. In Go, define
  interfaces where they are consumed, and only when there are two implementations or a test needs one.
- Factories, managers, services, wrappers and option structs that add a layer without a second user.
- Packages or folders named `utils`, `helpers`, `common`, `misc`.
- Config flags, parameters or generic type parameters nobody asked for.
- Re-implementing something the core or the standard library already provides.

**Errors and control flow**
- Checks for states the types or the caller already rule out.
- Errors swallowed or logged and then ignored. Go: return them wrapped with context,
  `fmt.Errorf("load invoice %d: %w", id, err)`. TS: no empty `catch {}`.
- `panic` in library code, `init()` with side effects, package-level mutable state.
- Goroutines without an owner, a `context` and a way to stop. `context.Background()` inside a request.

**Invented or stale APIs**
- Functions, options or flags that do not exist at the pinned version. Verify with `go doc` or the
  package's `.d.ts` (see `dependency-check`).

**TypeScript and React**
- `any`, `as unknown as T`, `@ts-ignore` / `@ts-expect-error` without a reason.
- `useEffect` that derives state from props or other state; compute it during render.
- `useMemo` / `useCallback` everywhere without a measured reason.
- Components that only forward props to one child.
- `console.log` left in, and API calls outside the generated client.

**Tests**
- Tests that assert the mock was called instead of the behavior.
- Many near-identical tests where one table-driven test would do.
- Snapshot tests of large output nobody will review.
- Missing the negative case: wrong user, invalid input, empty list.

**Naming**
- Vague names (`data`, `result`, `info`, `item`, `handleStuff`) where a domain word exists.
- Stacked suffixes (`UserServiceManagerImpl`).
- Synonym cycling: the same thing called `account`, `user` and `member` in one change.

**Logs and messages**
- Log lines on every step, emoji in logs, or messages that leak internals to users.
- Error messages for users: say what happened and what to do, in plain words.

## Size check

Compare the diff to the request. If the change adds more than it needs, cut it. Each changed line
should trace back to the request.

## Output

For a review, list findings as `path:line: pattern, fix`. When you fix your own diff, fix first, then
report only what changed. Do not rewrite code outside the diff.
