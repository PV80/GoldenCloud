#!/usr/bin/env python3
"""Check that required documents exist and that relative links between them resolve.

Run from the repository root:  python3 scripts/check_docs.py
Exits non-zero and prints every problem it found, rather than stopping at the first.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path
from urllib.parse import unquote, urlparse

ROOT = Path(__file__).resolve().parent.parent

REQUIRED = [
    "README.md",
    "DECISIONS.md",
    "PROGRESS.md",
    "ROADMAP.md",
    "LICENSE",
    "deploy/RUNBOOK.md",
    "docs/CLIENT-TEST.md",
]

# Inline links [text](target) and reference definitions [id]: target
LINK_RE = re.compile(r"\[[^\]]*\]\(\s*<?([^)\s>]+)>?(?:\s+\"[^\"]*\")?\s*\)")

SKIP_DIRS = {".git", "node_modules", "bin", "obj", "dist", ".github/ISSUE_TEMPLATE"}


def markdown_files() -> list[Path]:
    out = []
    for path in ROOT.rglob("*.md"):
        rel = path.relative_to(ROOT)
        if any(part in SKIP_DIRS for part in rel.parts):
            continue
        out.append(path)
    return sorted(out)


def main() -> int:
    problems: list[str] = []

    for name in REQUIRED:
        if not (ROOT / name).exists():
            problems.append(f"missing required document: {name}")

    for md in markdown_files():
        rel = md.relative_to(ROOT)
        text = md.read_text(encoding="utf-8")
        for target in LINK_RE.findall(text):
            parsed = urlparse(target)
            if parsed.scheme or target.startswith("//"):
                continue  # external URL — not our job to fetch
            path_part = unquote(parsed.path)
            if not path_part:
                continue  # pure #anchor
            base = ROOT if path_part.startswith("/") else md.parent
            resolved = (base / path_part.lstrip("/")).resolve()
            if not resolved.exists():
                problems.append(f"{rel}: broken relative link -> {target}")

    if problems:
        print(f"check_docs: {len(problems)} problem(s) found\n", file=sys.stderr)
        for p in problems:
            print(f"  - {p}", file=sys.stderr)
        return 1

    print(f"check_docs: OK ({len(markdown_files())} markdown files checked)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
