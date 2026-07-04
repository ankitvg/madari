#!/usr/bin/env python3
"""Reject SQL that is obviously outside the cloudsql-readonly skill boundary."""

from __future__ import annotations

import re
import sys


BLOCKED = {
    "alter",
    "analyze",
    "begin",
    "call",
    "commit",
    "copy",
    "create",
    "delete",
    "drop",
    "execute",
    "grant",
    "insert",
    "lock",
    "merge",
    "refresh",
    "reindex",
    "replace",
    "restore",
    "revoke",
    "rollback",
    "set",
    "truncate",
    "update",
    "vacuum",
}

ALLOWED_START = {"select", "with", "show", "describe", "explain"}


def strip_comments(sql: str) -> str:
    sql = re.sub(r"/\*.*?\*/", " ", sql, flags=re.DOTALL)
    sql = re.sub(r"--[^\n]*", " ", sql)
    return sql


def normalized_tokens(sql: str) -> list[str]:
    return re.findall(r"[A-Za-z_][A-Za-z0-9_]*", strip_comments(sql).lower())


def main() -> int:
    sql = " ".join(sys.argv[1:]).strip() if len(sys.argv) > 1 else sys.stdin.read().strip()
    if not sql:
        print("ERROR: no SQL provided", file=sys.stderr)
        return 2

    tokens = normalized_tokens(sql)
    if not tokens:
        print("ERROR: no SQL tokens found", file=sys.stderr)
        return 2

    first = tokens[0]
    if first not in ALLOWED_START:
        print(f"ERROR: statement starts with blocked or unsupported keyword: {first}", file=sys.stderr)
        return 1

    blocked = sorted({token for token in tokens if token in BLOCKED})
    if blocked:
        print("ERROR: blocked keyword(s): " + ", ".join(blocked), file=sys.stderr)
        return 1

    print("OK: SQL appears read-only")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
