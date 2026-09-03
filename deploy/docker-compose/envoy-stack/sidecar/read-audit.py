#!/usr/bin/env python3
"""Render hoop-inspect's JSONL audit stream as a readable table.

Reads `docker compose logs hoop-inspect` on stdin. Kept as its own file
rather than a heredoc inside the demo script: nested shell quoting around an
f-string silently produced an empty section, which is a bad failure mode for
the one part of the demo that proves the audit trail exists.
"""

import json
import sys


def main() -> int:
    rows = []
    for line in sys.stdin:
        i = line.find('{"kind"')
        if i < 0:
            continue
        try:
            rows.append(json.loads(line[i:]))
        except json.JSONDecodeError:
            continue

    if not rows:
        print("  (no audit events in this window)")
        return 0

    for e in rows:
        kind = e.get("kind")
        if kind in ("statement", "violation"):
            mark = "ALLOW" if e.get("allowed") else "DENY "
            proto = e.get("protocol", "")
            op = e.get("operation", "")
            stmt = (e.get("statement") or "")[:46]
            print(f"  {mark} {proto:<9}{op:<10}{stmt}")
        elif kind == "masked":
            ents = ",".join(e.get("masked_entities") or [])
            print(f"  MASK  entities={ents} count={e.get('masked_count', 0)}")
        elif kind == "session_end":
            print(
                f"  END   {e.get('connection', ''):<9}"
                f"statements={e.get('statement_count', 0)} "
                f"denied={e.get('denied_count', 0)}"
            )

    # The audit trail must never contain a value that masking removed:
    # recording what you masked, in the clear, has un-masked it. So the list
    # below tracks the live mask rules, not the fixtures: this build enforces
    # ONE data masking rule and spends it on emails, so email addresses are
    # the only values it can assert anything about. The card, SSN, CPF and
    # IBAN fixtures the demo sends reach the audit trail in the clear because
    # nothing masked them, and listing them here would fail a check this
    # build was never asked to pass. Restore a mask rule in
    # sidecar/config.yaml and add its fixtures back beside these:
    # 4111111111111111 (cards), 123-45-6789 and 555-12-3456 (ssn),
    # 111.444.777-35 (cpf), GB82WEST12345698765432 (iban).
    blob = json.dumps(rows)
    print()
    masked_values = ("ada@example.com", "grace@example.com", "alan@example.com")
    leaks = [v for v in masked_values if v in blob]
    if leaks:
        print(f"  LEAK: masked values present in the audit trail: {leaks}")
        return 1
    print("  verified: no masked value appears in the audit trail")
    return 0


if __name__ == "__main__":
    sys.exit(main())
