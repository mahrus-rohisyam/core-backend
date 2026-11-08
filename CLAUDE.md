# CLAUDE.md

@AGENTS.md

## Claude Code specifics

- Skills in `.claude/skills/` are symlinks to `.agents/skills/`. Add a new skill in `.agents/skills/`
  and link it, so Codex and Claude read the same file.
- Review subagents in `.claude/agents/`: `security-reviewer`, `dependency-auditor`, `slop-reviewer`.
  They are read-only. Run the relevant ones on a finished change before reporting it as done.
- `.claude/settings.json` runs `scripts/agent/check-hidden-unicode.py` after every file write. When it
  reports a character, remove it unless the user says it is intended.
- Installing or adding packages (`go get`, `npm install`, `npx`, ...) asks the user first.
  Do the `dependency-check` skill before you request it.
