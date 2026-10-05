"""python-oracledb in thin mode through envoy:1521.

Runs from the oraclethin container. Usage:
    python thin.py select             # the fixture, emails masked
    python thin.py bind               # a bind-variable lookup, email masked
    python thin.py denied             # the taxpayer-id DELETE, ORA-01031
    python thin.py count              # rows left in customers
"""

import os
import sys

import oracledb

DSN = "envoy:1521/" + os.environ.get("ORACLE_SERVICE", "FREEPDB1")


def main(mode: str) -> int:
    conn = oracledb.connect(user="appuser", password="apppass", dsn=DSN)
    assert conn.thin, "python-oracledb fell back to thick mode"
    cur = conn.cursor()
    if mode == "denied":
        # The relay writes ORA-01031 and then closes the session, so the
        # connection is gone afterwards; do not close it again.
        try:
            cur.execute("DELETE FROM customers WHERE cpf = '111.444.777-35'")
        except oracledb.DatabaseError as exc:
            print(f"  {exc}".splitlines()[0])
            return 0
        print("  DELETE was not refused")
        return 1
    with conn:
        if mode == "select":
            cur.execute("SELECT id, name, email, ssn FROM customers ORDER BY id")
            for row in cur:
                print("  " + " | ".join("" if v is None else str(v) for v in row))
        elif mode == "bind":
            cur.execute("SELECT name, email FROM customers WHERE id = :id", id=2)
            print("  " + " | ".join(cur.fetchone()))
        elif mode == "count":
            cur.execute("SELECT COUNT(*) FROM customers")
            print(cur.fetchone()[0])
        else:
            print(f"unknown mode {mode}", file=sys.stderr)
            return 2
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1] if len(sys.argv) > 1 else "select"))
