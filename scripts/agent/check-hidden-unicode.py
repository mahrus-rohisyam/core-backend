#!/usr/bin/env python3
"""Report invisible and bidirectional-control Unicode characters in text files.

These characters can hide code from reviewers (Trojan Source, CVE-2021-42574)
or carry instructions to coding agents that a human never sees.

Usage:
  check-hidden-unicode.py FILE...            check the given files
  git ls-files -z | xargs -0 check-hidden-unicode.py
  (no arguments)                             Claude Code hook: read the hook JSON
                                             on stdin and check the written file

Exit status: 0 clean, 2 characters found (a PostToolUse hook passes stderr
back to the agent), 1 usage error. Binary, non-UTF-8 and very large files are
skipped. Standard library only.
"""

import json
import os
import sys
import unicodedata

MAX_BYTES = 5 * 1024 * 1024

RANGES = (
    (0x00AD, 0x00AD),    # soft hyphen
    (0x061C, 0x061C),    # Arabic letter mark
    (0x115F, 0x1160),    # Hangul fillers
    (0x180E, 0x180E),    # Mongolian vowel separator
    (0x200B, 0x200F),    # zero-width space/joiners, LRM, RLM
    (0x202A, 0x202E),    # bidi embeddings and overrides
    (0x2060, 0x2064),    # word joiner, invisible operators
    (0x2066, 0x2069),    # bidi isolates
    (0x3164, 0x3164),    # Hangul filler
    (0xFEFF, 0xFEFF),    # zero-width no-break space / BOM
    (0xFFA0, 0xFFA0),    # halfwidth Hangul filler
    (0xE0000, 0xE007F),  # tag characters
)


def suspicious(ch):
    cp = ord(ch)
    return any(lo <= cp <= hi for lo, hi in RANGES)


def check(path):
    try:
        if os.path.getsize(path) > MAX_BYTES:
            return []
        with open(path, "rb") as f:
            raw = f.read()
    except OSError:
        return []
    if b"\0" in raw[:8192]:
        return []
    try:
        text = raw.decode("utf-8")
    except UnicodeDecodeError:
        return []
    hits = []
    for lineno, line in enumerate(text.splitlines(), 1):
        for col, ch in enumerate(line, 1):
            if suspicious(ch):
                name = unicodedata.name(ch, "UNNAMED")
                hits.append(f"{path}:{lineno}:{col}: U+{ord(ch):04X} {name}")
    return hits


def paths_from_hook():
    try:
        event = json.load(sys.stdin)
    except ValueError:
        return []
    tool_input = event.get("tool_input") or {}
    path = tool_input.get("file_path") or tool_input.get("notebook_path")
    return [path] if path else []


def main(argv):
    paths = argv[1:] or paths_from_hook()
    if not paths:
        return 0
    hits = [hit for path in paths for hit in check(path)]
    if not hits:
        return 0
    print("Hidden Unicode characters found (remove them unless the user says they are intended):",
          file=sys.stderr)
    for hit in hits[:50]:
        print("  " + hit, file=sys.stderr)
    if len(hits) > 50:
        print(f"  ... and {len(hits) - 50} more", file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
