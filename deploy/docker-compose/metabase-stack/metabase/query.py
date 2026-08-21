#!/usr/bin/env python3
"""Run queries THROUGH Metabase, the way the browser does.

./demo.sh could talk to the sidecar with psql and prove masking in three
lines. It would also prove nothing about Metabase: the interesting claim is
that redaction survives Metabase's own query pipeline (its driver, its
result serialization, its download endpoint), and the only way to test that
is to make Metabase issue the query.

So every masked value ./demo.sh shows came back through the same API the
Metabase UI calls, and each command below is one of the three paths a user has
to the data:

    table   the query builder, where no SQL is typed
    sql     the native SQL editor, the hole Pro's column security leaves open
    csv     a download, where the rows leave Metabase entirely

Credentials, the database record and the HTTP plumbing are imported from
provision.py so the two cannot disagree about what was set up.

Usage:
    query.py table <table-name> [limit]
    query.py sql   "<SQL>"
    query.py csv   "<SQL>"        raw CSV on stdout
"""

import json
import sys
import urllib.error
import urllib.parse
import urllib.request

from provision import DATABASE, call, die, login

BASE = "http://localhost:3000"


def database_id(session):
    status, body = call(BASE, "/api/database", session=session)
    if status != 200:
        die(f"could not list databases (HTTP {status}): {body}")
    rows = body.get("data", []) if isinstance(body, dict) else body
    for db in rows or []:
        if db.get("name") == DATABASE["name"]:
            return db["id"]
    die(f"database {DATABASE['name']!r} not found; run ./run.sh first")


def table_id(session, db_id, name):
    status, body = call(BASE, f"/api/database/{db_id}/metadata", session=session)
    if status != 200:
        die(f"could not read database metadata (HTTP {status}): {body}")
    for t in body.get("tables") or []:
        if t.get("name") == name:
            return t["id"]
    die(f"table {name!r} not synced yet; check: docker compose logs metabase")


def run(session, payload):
    """POST /api/dataset and return (columns, rows).

    Metabase answers 202 with a result object for a query the database
    refused, so a failure is a field in the body and not an HTTP status.
    """
    status, body = call(BASE, "/api/dataset", method="POST", body=payload, session=session)
    if not isinstance(body, dict):
        die(f"unexpected /api/dataset response (HTTP {status}): {body}")
    if body.get("status") == "failed" or body.get("error"):
        # The interesting case, not an error case: this is how an operator's
        # policy message reaches the analyst's screen. Printed and returned
        # empty so the caller can keep going.
        print(f"  Metabase reports: {body.get('error') or body.get('status')}")
        return [], []
    data = body.get("data") or {}
    cols = [c.get("display_name") or c.get("name") for c in data.get("cols") or []]
    return cols, data.get("rows") or []


def show(cols, rows):
    if not cols:
        return
    widths = [len(c) for c in cols]
    text = [[("" if v is None else str(v)) for v in r] for r in rows]
    for r in text:
        for i, v in enumerate(r[: len(widths)]):
            widths[i] = max(widths[i], len(v))
    line = "  " + "  ".join(c.ljust(w) for c, w in zip(cols, widths))
    print(line)
    print("  " + "  ".join("-" * w for w in widths))
    for r in text:
        print("  " + "  ".join(v.ljust(w) for v, w in zip(r, widths)))


def export_csv(session, payload):
    """POST /api/dataset/csv, Metabase's download endpoint.

    Form-encoded rather than JSON: this route predates the JSON API surface
    and still takes the query as a `query` form field. The response is raw
    CSV, so it bypasses the JSON helper entirely.
    """
    data = urllib.parse.urlencode({"query": json.dumps(payload)}).encode()
    req = urllib.request.Request(f"{BASE}/api/dataset/csv", data=data, method="POST")
    req.add_header("Content-Type", "application/x-www-form-urlencoded")
    req.add_header("X-Metabase-Session", session)
    try:
        # Generous: 5,000 rows is a real export and Metabase streams it.
        with urllib.request.urlopen(req, timeout=180) as resp:
            return resp.read().decode()
    except urllib.error.HTTPError as e:
        die(f"CSV export failed (HTTP {e.code}): {e.read().decode()[:400]}")


def main():
    if len(sys.argv) < 3:
        die(__doc__.strip().split("Usage:")[-1].strip())
    cmd, arg = sys.argv[1], sys.argv[2]

    session = login(BASE)
    db_id = database_id(session)

    if cmd == "table":
        limit = int(sys.argv[3]) if len(sys.argv) > 3 else 10
        payload = {
            "database": db_id,
            "type": "query",
            "query": {"source-table": table_id(session, db_id, arg), "limit": limit},
        }
        show(*run(session, payload))

    elif cmd in ("sql", "csv"):
        payload = {"database": db_id, "type": "native", "native": {"query": arg}}
        if cmd == "sql":
            show(*run(session, payload))
        else:
            sys.stdout.write(export_csv(session, payload))

    else:
        die(f"unknown command {cmd!r}")


if __name__ == "__main__":
    main()
