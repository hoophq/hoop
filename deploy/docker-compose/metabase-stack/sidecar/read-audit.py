#!/usr/bin/env python3
"""Summarize hoop-inspect's JSONL audit stream for the Metabase stack.

Reads `docker compose logs hoop-inspect` on stdin.

../envoy-stack prints every event, which works for a demo of eight statements.
Metabase issues hundreds (a sync fingerprints every column; a dashboard fans
out one query per card), so printing them all would bury the denials. This one
counts them and shows only the interesting rows.
"""

import json
import re
import sys
from collections import Counter
from pathlib import Path

# No value that masking removed may appear here in the clear: an audit trail
# that records one un-masks it.
#
# The values are read out of the seed rather than written down, because a list
# copied from another file goes stale the first time somebody adds a row, and
# this check exists to catch exactly the value nobody remembered.
SEED = Path(__file__).resolve().parent.parent / "upstream" / "seed.sql"

# Columns ../sidecar/config.yaml masks. One mask rule for the whole process,
# spent on the EMAIL_ADDRESS entity, so these two columns and nothing else.
#
# ssn, cpf, iban and bank_iban are NOT listed, and leaving them in would be a
# check for a property this build does not have: their rules are commented out
# over the limit, the values reach Metabase as stored, and the audit trail
# recording them is then correct rather than a leak. Restore a mask rule and
# add its columns back here in the same edit; the check is only worth its
# runtime while the two lists agree.
PII_COLUMNS = {"email", "actor_email"}

# events is filled by a SELECT rather than by literal tuples, so the parser
# below never sees its addresses. One of the generated series stands in for all
# 5,000; ./demo.sh asserts the other 4,999 against the CSV export.
GENERATED = ("user00001@example.com",)

_INSERT = re.compile(r"INSERT\s+INTO\s+(\w+)\s*\(([^)]*)\)\s*VALUES(.*?);", re.I | re.S)
_TUPLE = re.compile(r"\(([^()]*)\)")
# A field is a quoted literal, doubled quotes included, or a bare token. The
# quoted branch runs first, so a comma inside a value does not split a row.
_FIELD = re.compile(r"'((?:[^']|'')*)'|([^,\s][^,]*)")


def seeded_secrets(path):
    """Every literal in seed.sql that sits in a column this lane masks."""
    try:
        body = path.read_text()
    except OSError as err:
        raise SystemExit(f"cannot read {path}: {err}")

    found = set()
    for stmt in _INSERT.finditer(body):
        table, columns, values = stmt.group(1), stmt.group(2), stmt.group(3)
        names = [c.strip().lower() for c in columns.split(",")]
        for tup in _TUPLE.findall(values):
            row = [
                (m.group(1) if m.group(1) is not None else m.group(2)).replace("''", "'").strip()
                for m in _FIELD.finditer(tup)
            ]
            # Fail loudly. A row this parser reads wrong is a row it stops
            # covering, and a leak check that covers nothing still passes.
            if len(row) != len(names):
                raise SystemExit(
                    f"{path}: cannot parse a row of {table}: "
                    f"{len(row)} fields against {len(names)} columns"
                )
            found.update(
                value
                for name, value in zip(names, row)
                if name in PII_COLUMNS and value and value.upper() != "NULL"
            )

    if not found:
        raise SystemExit(f"{path}: found no maskable values, so the leak check proves nothing")
    return found


def readable(sql):
    """Split a Metabase statement into (sql, actor).

    Metabase prefixes every query with a comment naming the user:

        -- Metabase:: userID: 1 queryType: native queryHash: 351c93b5...
        SELECT ...

    It has to come off, or a truncated line shows the header and none of the
    SQL, and it is worth keeping: hoop-inspect authenticates one shared
    database credential, while Metabase puts the human's id on the wire.
    queryHash is dropped as a 64-hex-digit digest that crowds the line.
    """
    actor = ""
    lines = []
    for line in sql.splitlines():
        s = line.strip()
        if s.startswith("--"):
            if "Metabase::" in s:
                actor = s.split("Metabase::", 1)[1].split("queryHash:")[0].strip()
            continue
        if s:
            lines.append(s)
    return " ".join(lines), actor


def main() -> int:
    events = []
    for line in sys.stdin:
        i = line.find('{"kind"')
        if i < 0:
            continue
        try:
            events.append(json.loads(line[i:]))
        except json.JSONDecodeError:
            continue

    if not events:
        print("  (no audit events in this window)")
        return 0

    ops = Counter()
    entities = Counter()
    allowed = denied = results = masked_values = 0
    denials, masked = [], []

    for e in events:
        kind = e.get("kind")
        if kind in ("statement", "violation"):
            # A server-direction statement is a RESULT SET: its text is the
            # command tag ("SELECT 3") and its operation is always unknown,
            # because a response carries no verb. Counting them as statements
            # would report a quarter of Metabase's traffic unclassifiable.
            if e.get("direction") == "server":
                results += 1
                continue
            ops[e.get("operation") or "?"] += 1
            if e.get("allowed"):
                allowed += 1
            else:
                denied += 1
                denials.append(e)
        elif kind == "masked":
            masked_values += e.get("masked_count", 0)
            for ent in e.get("masked_entities") or []:
                entities[ent] += 1
            masked.append(e)

    verbs = ", ".join(f"{n} {op}" for op, n in ops.most_common(6))
    print(f"  {allowed + denied} statements recorded ({verbs})")
    print(f"  {allowed} allowed, {denied} denied, {results} result sets")
    if masked_values:
        seen = ", ".join(f"{ent}" for ent, _ in entities.most_common())
        print(f"  {masked_values} values masked across {len(masked)} result sets ({seen})")

    for e in denials:
        sql, actor = readable(e.get("statement") or "")
        print()
        print(f"  DENY  {sql[:64]}")
        print(f"        rule={e.get('rule', '?')}  {e.get('message', '')}")
        if actor:
            print(f"        metabase {actor}")

    # The audit trail must never contain a value that masking removed.
    forbidden = seeded_secrets(SEED) | set(GENERATED)
    blob = json.dumps(events)
    leaks = sorted(v for v in forbidden if v in blob)
    print()
    if leaks:
        print(f"  LEAK: {len(leaks)} masked values present in the audit trail:")
        for value in leaks:
            print(f"        {value}")
        return 1
    print(f"  verified: none of the {len(forbidden)} seeded values appears in the audit trail")
    return 0


if __name__ == "__main__":
    sys.exit(main())
