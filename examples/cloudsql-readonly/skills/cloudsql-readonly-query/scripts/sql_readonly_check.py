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
    "into",
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


class SQLCheckError(ValueError):
    pass


def scrub_sql(sql: str) -> str:
    out: list[str] = []
    i = 0
    while i < len(sql):
        ch = sql[i]
        nxt = sql[i + 1] if i + 1 < len(sql) else ""

        if ch == "'":
            out.append(" ")
            i += 1
            while i < len(sql):
                if sql[i] == "\\":
                    i += 2
                    continue
                if sql[i] == "'":
                    if i + 1 < len(sql) and sql[i + 1] == "'":
                        i += 2
                        continue
                    i += 1
                    break
                i += 1
            else:
                raise SQLCheckError("unterminated string literal")
            continue

        if ch in {'"', "`"}:
            quote = ch
            out.append(" ")
            i += 1
            while i < len(sql):
                if sql[i] == quote:
                    if i + 1 < len(sql) and sql[i + 1] == quote:
                        i += 2
                        continue
                    i += 1
                    break
                i += 1
            else:
                raise SQLCheckError("unterminated quoted identifier")
            continue

        if ch == "$":
            match = re.match(r"\$[A-Za-z_][A-Za-z0-9_]*\$|\$\$", sql[i:])
            if match:
                tag = match.group(0)
                end = sql.find(tag, i + len(tag))
                if end == -1:
                    raise SQLCheckError("unterminated dollar-quoted string")
                out.append(" ")
                i = end + len(tag)
                continue

        if ch == "[":
            end = sql.find("]", i + 1)
            if end != -1:
                out.append(" ")
                i = end + 1
                continue

        if ch == "/" and nxt == "*":
            if i + 2 < len(sql) and sql[i + 2] == "!":
                raise SQLCheckError("MySQL executable comments are not allowed")
            end = sql.find("*/", i + 2)
            if end == -1:
                raise SQLCheckError("unterminated block comment")
            out.append(" ")
            i = end + 2
            continue

        if ch == "-" and nxt == "-":
            end = sql.find("\n", i + 2)
            if end == -1:
                break
            out.append("\n")
            i = end + 1
            continue

        out.append(ch)
        i += 1

    return "".join(out)


def normalized_tokens(sql: str) -> list[str]:
    return re.findall(r"[A-Za-z_][A-Za-z0-9_]*", scrub_sql(sql).lower())


def find_row_locking_clause(tokens: list[str]) -> str | None:
    for i, token in enumerate(tokens):
        if token != "for" or i + 1 >= len(tokens):
            continue
        next_token = tokens[i + 1]
        if next_token == "update":
            return "for update"
        if next_token == "share":
            return "for share"
        if next_token == "key" and i + 2 < len(tokens) and tokens[i + 2] == "share":
            return "for key share"
        if next_token == "no" and i + 3 < len(tokens) and tokens[i + 2] == "key" and tokens[i + 3] == "update":
            return "for no key update"
    return None


def main() -> int:
    sql = " ".join(sys.argv[1:]).strip() if len(sys.argv) > 1 else sys.stdin.read().strip()
    if not sql:
        print("ERROR: no SQL provided", file=sys.stderr)
        return 2

    try:
        tokens = normalized_tokens(sql)
    except SQLCheckError as err:
        print(f"ERROR: {err}", file=sys.stderr)
        return 1
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
    if row_lock := find_row_locking_clause(tokens):
        print(f"ERROR: row-locking SELECT clause is not allowed: {row_lock}", file=sys.stderr)
        return 1

    print("OK: SQL appears read-only")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
