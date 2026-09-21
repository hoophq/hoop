#!/usr/bin/env python3
"""Print the reason behind every session hoop-inspect refused to follow.

Reads `docker compose logs hoop-inspect` on stdin. A refusal at the handshake
never becomes a statement, so the audit table in
../../envoy-stack/sidecar/read-audit.py shows it only as a session that ended
with denied=1. The relay's own log line carries the operator-facing reason,
under rule "stream-unsafe"; this prints that reason and nothing else.
"""

import json
import sys


def main() -> int:
    seen = 0
    for line in sys.stdin:
        i = line.find('{"time"')
        if i < 0:
            continue
        try:
            e = json.loads(line[i:])
        except json.JSONDecodeError:
            continue
        if e.get("msg") != "statement denied" or e.get("rule") != "stream-unsafe":
            continue
        seen += 1
        msg = e.get("message", "")
        # The codec prefixes the sentinel's text; the part after the colon is
        # the reason written for the operator.
        msg = msg.split("bypass the relay: ", 1)[-1]
        print(f"  {seen}. {msg}")
        print()
    if not seen:
        print("  (no refused sessions in this window)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
