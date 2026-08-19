#!/usr/bin/env python3
"""Summarize hoop-inspect's JSONL audit stream for the Metabase stack.

Reads `docker compose logs hoop-inspect` on stdin.

../envoy-stack prints every event, which works for a demo of eight statements.
Metabase issues hundreds (a sync fingerprints every column; a dashboard fans
out one query per card), so printing them all would bury the denials. This one
counts them and shows only the interesting rows.
"""

import json
import sys
from collections import Counter

# Values that exist in upstream/seed.sql and must never appear here. The audit
# trail recording, in the clear, a value that masking removed would un-mask it.
FORBIDDEN = ("ada@example.com", "user00001@example.com", "555-12-3456")


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
    blob = json.dumps(events)
    leaks = [v for v in FORBIDDEN if v in blob]
    print()
    if leaks:
        print(f"  LEAK: masked values present in the audit trail: {leaks}")
        return 1
    print("  verified: no masked value appears in the audit trail")
    return 0


if __name__ == "__main__":
    sys.exit(main())
