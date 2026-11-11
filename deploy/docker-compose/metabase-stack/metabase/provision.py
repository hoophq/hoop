#!/usr/bin/env python3
"""Take Metabase through its setup wizard and register the hoop-inspect lane.

Metabase can be provisioned declaratively from a config file, but that is a
Pro/Enterprise feature. On OSS the only supported path is the setup API, so
this script drives it:

    1. wait for /api/health
    2. read the one-time setup token from /api/session/properties
    3. POST /api/setup            create the admin account
    4. POST /api/session          log in, explicitly
    5. POST /api/database         register appdb THROUGH hoop-inspect
    6. poll until the schema sync finishes

The alternative, writing rows into Metabase's application database, would
be faster and would be a hack: that schema is internal, undocumented and
changes between releases. This uses the same API the browser uses.

Step 4 looks redundant, since POST /api/setup already returns a session. It is
deliberate: /api/session is a long-stable endpoint with a documented response,
so pinning the login to it means only ONE call here depends on the setup API's
exact response shape, and that call's failure is unambiguous.

Standard library only, matching the rest of the stack's prerequisites.

Usage: provision.py <base-url>
"""

import json
import sys
import time
import urllib.error
import urllib.request

# Matches the credentials ./run.sh prints. Not a secret: this stack seeds fake
# customers and binds to localhost.
ADMIN = {
    "first_name": "Demo",
    "last_name": "User",
    "email": "demo@hoop.dev",
    "password": "hoopdemo1",
}
SITE_NAME = "Hoop Demo"

# The whole integration, in one object. Note what is NOT here: no plugin, no
# driver, no Metabase-side code. `host` and `port` are the sidecar instead of
# the database, and that is the entire change a real deployment makes too.
DATABASE = {
    "engine": "postgres",
    "name": "appdb (via hoop-inspect)",
    "details": {
        "host": "hoop-inspect",
        "port": 15432,
        "dbname": "appdb",
        "user": "appuser",
        "password": "apppass",
        # The lane is plaintext downstream: Metabase and the sidecar share a
        # container network here. A deployment crossing an untrusted network
        # (Metabase Cloud above all) sets downstream_tls on the lane and
        # flips this to true. See the README's reference section.
        "ssl": False,
        "tunnel-enabled": False,
    },
    "is_full_sync": True,
}

HEALTH_TIMEOUT = 300  # Metabase migrates an empty H2 database on first boot.
SYNC_TIMEOUT = 180


def ok(msg):
    print(f"\033[32m  ok\033[0m  {msg}", flush=True)


def die(msg):
    print(f"\033[31mfail\033[0m {msg}", file=sys.stderr, flush=True)
    sys.exit(1)


def call(base, path, method="GET", body=None, session=None):
    """One HTTP call. Returns (status, decoded-json-or-raw-text)."""
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(f"{base}{path}", data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if session:
        req.add_header("X-Metabase-Session", session)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            raw = resp.read().decode()
            return resp.status, (json.loads(raw) if raw else None)
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        try:
            return e.code, json.loads(raw)
        except json.JSONDecodeError:
            return e.code, raw
    except (urllib.error.URLError, TimeoutError) as e:
        return None, str(e)


def wait_for_health(base):
    deadline = time.time() + HEALTH_TIMEOUT
    while time.time() < deadline:
        status, _ = call(base, "/api/health")
        if status == 200:
            ok(f"Metabase is up at {base}")
            return
        time.sleep(3)
    die(f"Metabase did not answer /api/health within {HEALTH_TIMEOUT}s")


def create_admin(base):
    """Run the setup wizard. Returns True if it ran, False if already done."""
    status, props = call(base, "/api/session/properties")
    if status != 200:
        die(f"could not read /api/session/properties (HTTP {status}): {props}")

    token = props.get("setup-token")
    if not token:
        # Already provisioned. Re-running ./run.sh on a live stack is a normal
        # thing to do, so this is a skip and not an error.
        ok("admin already exists, skipping setup")
        return False

    status, body = call(
        base,
        "/api/setup",
        method="POST",
        body={
            "token": token,
            "user": ADMIN,
            "prefs": {"site_name": SITE_NAME},
        },
    )
    if status not in (200, 201):
        die(
            f"POST /api/setup failed (HTTP {status}): {body}\n"
            "      The setup API is version-sensitive and this stack pins\n"
            "      metabase/metabase in docker-compose.yml. If you bumped\n"
            "      that tag, the payload above may need updating."
        )
    ok(f"admin  {ADMIN['email']} / {ADMIN['password']}")
    return True


def login(base):
    status, body = call(
        base,
        "/api/session",
        method="POST",
        body={"username": ADMIN["email"], "password": ADMIN["password"]},
    )
    if status not in (200, 201) or not isinstance(body, dict) or "id" not in body:
        die(f"could not log in as {ADMIN['email']} (HTTP {status}): {body}")
    return body["id"]


def register_database(base, session):
    """Register the lane, or find it if a previous run already did."""
    status, existing = call(base, "/api/database", session=session)
    if status == 200:
        # The payload is {"data": [...]} on current versions and a bare list
        # on older ones. Accept either rather than guessing.
        rows = existing.get("data", []) if isinstance(existing, dict) else existing
        for db in rows or []:
            if db.get("name") == DATABASE["name"]:
                ok(f"database {DATABASE['name']!r} already registered (id {db['id']})")
                return db["id"]

    status, body = call(base, "/api/database", method="POST", body=DATABASE, session=session)
    if status not in (200, 201) or not isinstance(body, dict) or "id" not in body:
        die(
            f"POST /api/database failed (HTTP {status}): {body}\n"
            "      Metabase tests the connection before saving, so this is\n"
            "      usually the sidecar refusing or the lane being down.\n"
            "      Check: curl -s localhost:19000/config"
        )
    ok(f"database {DATABASE['name']!r} registered (id {body['id']})")
    return body["id"]


def wait_for_sync(base, session, db_id):
    """Block until the schema sync finishes.

    Without this ./run.sh would return before Metabase knows the tables exist,
    and the first thing anyone did would be to look at an empty data browser
    and conclude the proxy ate their schema.
    """
    deadline = time.time() + SYNC_TIMEOUT
    while time.time() < deadline:
        status, db = call(base, f"/api/database/{db_id}", session=session)
        if status == 200:
            state = db.get("initial_sync_status")
            if state == "complete":
                ok("schema sync complete")
                return
            if state == "aborted":
                die("Metabase aborted the schema sync; see: docker compose logs metabase")
        time.sleep(3)
    # Not fatal. Sync is asynchronous and slow syncs are a Metabase property,
    # not a proxy failure; the stack is usable and the tables will appear.
    print(
        "\033[33mwarn\033[0m schema sync still running after "
        f"{SYNC_TIMEOUT}s; tables will appear as it finishes",
        flush=True,
    )


def main():
    if len(sys.argv) != 2:
        die("usage: provision.py <base-url>")
    base = sys.argv[1].rstrip("/")

    wait_for_health(base)
    create_admin(base)
    session = login(base)
    db_id = register_database(base, session)
    wait_for_sync(base, session, db_id)


if __name__ == "__main__":
    main()
