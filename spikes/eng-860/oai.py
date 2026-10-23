#!/usr/bin/env python3
"""ENG-860 Agents API driver. Throwaway. Reads the key from a file; never prints it.

  oai.py get <path>
  oai.py post <path> <json-file|->      prints status and body; a 4xx is a result, not a crash
  oai.py delete <path>
  oai.py run <session.json> <out.json> [prompt.txt]
  oai.py say <session_id> <prompt.txt> <out.json>
  oai.py items <session_id>
  oai.py end <session_id>

Session files and prompts may hold markers: {{NAME}} is the environment
variable NAME, {{B64:path}} a file under this folder in base64 (its own
markers filled), {{SECRET:name}} stack/.secrets/name, {{SESSION_ID}} the new
session's id. A prompt with {{SESSION_ID}} is sent after the session exists.
A request body with a secret is never printed or saved: post prints only
the response, and the API never returns secret values.
"""
import base64
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
KEY = open(os.path.expanduser(os.environ.get("OAI_KEY_FILE", "~/.hoop/secrets/openai.key"))).read().strip()
BASE = "https://api.openai.com/v1"
HDRS = {"Authorization": "Bearer " + KEY, "OpenAI-Beta": "agents=v1", "Content-Type": "application/json"}


def call(method, path, body=None, fatal=True):
    req = urllib.request.Request(BASE + path, method=method, headers=HDRS,
                                 data=None if body is None else json.dumps(body).encode())
    try:
        with urllib.request.urlopen(req, timeout=120) as r:
            raw = r.read()
            return r.status, json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        if fatal:
            sys.exit(f"{method} {path}: HTTP {e.code}: {raw[:3000]}")
        try:
            return e.code, json.loads(raw)
        except ValueError:
            return e.code, {"raw": raw[:3000]}


def fill(text, sid=""):
    def sub(m):
        k, arg = m.group(1), m.group(2)
        if k == "SESSION_ID":
            return sid or m.group(0)
        if k == "B64":
            # A sandbox file, its own markers filled first.
            return base64.b64encode(fill(open(os.path.join(HERE, arg)).read(), sid).encode()).decode()
        if k == "SECRET":
            return open(os.path.join(HERE, "stack", ".secrets", arg)).read().strip()
        if k not in os.environ:
            sys.exit(f"marker {m.group(0)} has no environment variable {k}")
        return os.environ[k]
    return re.sub(r"\{\{([A-Z_0-9]+)(?::([A-Za-z0-9_./-]+))?\}\}", sub, text)


def message(text):
    return {"type": "agent.session.input.message",
            "input": [{"role": "user", "content": [{"type": "input_text", "text": text}]}]}


def items(sid):
    out, after = [], None
    while True:
        q = f"/agents/sessions/{sid}/items?order=asc&limit=100" + (f"&after={after}" if after else "")
        _, r = call("GET", q)
        out += r.get("data", [])
        if not r.get("has_more"):
            return out
        after = r["last_id"]


def show(it):
    t = it.get("type")
    if t == "command_execution":
        print(f"--> $ {it.get('command')}")
        print(f"<-- exit={it.get('exit_code')} status={it.get('status')}\n{(it.get('output') or '').rstrip()}")
    elif t == "mcp_call":
        print(f"--> mcp {it.get('server_label')}.{it.get('name')} {json.dumps(it.get('arguments'))}")
        print(f"<-- {json.dumps(it.get('output'))[:3000]} error={json.dumps(it.get('error'))}")
    elif t in ("message", "agent_message"):
        text = " ".join(c.get("text", "") for c in it.get("content") or [] if isinstance(c, dict))
        print(f"[{it.get('role', t)}] {text[:3000]}")
    elif t == "reasoning":
        pass
    else:
        print(f"[{t}] {json.dumps(it)[:600]}")


def last_turn(sid):
    _, turns = call("GET", f"/agents/sessions/{sid}/turns?order=desc&limit=1")
    return (turns.get("data") or [None])[0]


def wait(sid, seen=0, prev=None, deadline=900):
    """Waits for a turn newer than prev to end, then prints the items after seen."""
    t0 = time.time()
    while time.time() - t0 < deadline:
        last = last_turn(sid)
        if last and last["id"] != prev and last["status"] in ("completed", "failed", "cancelled"):
            break
        _, s = call("GET", f"/agents/sessions/{sid}")
        if s["status"] == "failed":
            print("session failed:", s.get("error"))
            break
        time.sleep(4)
    else:
        print("TIMEOUT waiting for the turn")
    _, s = call("GET", f"/agents/sessions/{sid}")
    _, turns = call("GET", f"/agents/sessions/{sid}/turns?order=asc&limit=100")
    its = items(sid)
    for it in its[seen:]:
        show(it)
    for t in turns.get("data", []):
        print(f"[turn {t['id']}] status={t['status']} error={json.dumps(t.get('error'))} usage={json.dumps(t.get('usage'))}")
    print(f"[session] status={s['status']} spend={json.dumps(s.get('spend_control'))}")
    return s, turns, its


def main():
    cmd, a = sys.argv[1], sys.argv[2:]
    if cmd == "get":
        print(json.dumps(call("GET", a[0])[1], indent=2))
    elif cmd == "delete":
        st, r = call("DELETE", a[0], fatal=False)
        print(st, json.dumps(r, indent=2))
    elif cmd == "post":
        src = sys.stdin.read() if a[1] == "-" else open(a[1]).read()
        st, r = call("POST", a[0], json.loads(fill(src)), fatal=False)
        print(st, json.dumps(r, indent=2))
    elif cmd == "run":
        body = json.loads(fill(open(a[0]).read()))
        prompt = open(a[2]).read() if len(a) > 2 else None
        late = prompt is not None and "{{SESSION_ID}}" in prompt
        if prompt is not None and not late:
            body["input"] = fill(prompt)
        st, s = call("POST", "/agents/sessions", body, fatal=False)
        if st >= 300:
            print(f"create refused: HTTP {st}\n{json.dumps(s, indent=2)}")
            json.dump({"request": body, "status": st, "response": s}, open(a[1], "w"), indent=2)
            return
        sid = s["id"]
        print(f"session {sid} environment={json.dumps(s.get('environment'))[:1500]}")
        if late:
            call("POST", f"/agents/sessions/{sid}/events", {"events": [message(fill(prompt, sid))]})
            time.sleep(3)
        s2, turns, its = wait(sid)
        json.dump({"request": body, "session": s2, "turns": turns, "items": its}, open(a[1], "w"), indent=2)
    elif cmd == "say":
        sid = a[0]
        seen, prev = len(items(sid)), (last_turn(sid) or {}).get("id")
        call("POST", f"/agents/sessions/{sid}/events", {"events": [message(fill(open(a[1]).read(), sid))]})
        time.sleep(3)
        s, turns, its = wait(sid, seen, prev)
        json.dump({"session": s, "turns": turns, "items": its}, open(a[2], "w"), indent=2)
    elif cmd == "items":
        for it in items(a[0]):
            show(it)
    elif cmd == "end":
        for i in range(10):
            st, r = call("DELETE", f"/agents/sessions/{a[0]}", fatal=False)
            if st != 409:
                print(st, json.dumps(r)[:300])
                return
            time.sleep(5)
        print("still 409 after 10 tries")
    else:
        sys.exit(__doc__)


if __name__ == "__main__":
    main()
