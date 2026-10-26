#!/usr/bin/env python3
"""ENG-880 upstream probe, from ENG-860. Throwaway.

Serves a small PII API, an echo route, a stateless MCP server and a
WebSocket echo. Logs every request as JSON before any check. A known
secret (env PROBE_SECRET_<name>) is never logged: it shows as
<REAL:name>, so a log line tells whether the vault filled a placeholder.

  /users, /users/<id>   PII JSON (the sidecar masks it)
  /echo                 what this server saw, secrets replaced
  /mcp                  MCP over streamable HTTP, JSON responses
  /ws                   WebSocket echo
"""
import base64
import hashlib
import json
import os
import struct
import sys
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# Fargate mounts no files: secrets and the log go through the environment
# and stdout (CloudWatch).
LOG = open(os.environ["PROBE_LOG"], "a", buffering=1) if os.environ.get("PROBE_LOG") else None
WATCH = ("authorization", "x-api-key", "x-hoop-token", "x-probe", "x-amzn-bedrock-agentcore-runtime-session-id", "x-hoop-session-id", "upgrade",
         "sec-websocket-protocol", "x-forwarded-for", "via", "cf-ray", "cdn-loop")

SECRETS = {k[len("PROBE_SECRET_"):]: v.strip() for k, v in os.environ.items()
           if k.startswith("PROBE_SECRET_") and v.strip()}

USERS = [
    {"id": 1, "name": "Grace Hopper", "email": "grace@example.com", "phone": "+1 415 555 0101",
     "card": "4111 1111 1111 1111", "plan": "enterprise"},
    {"id": 2, "name": "Alan Turing", "email": "alan@example.com", "phone": "+1 415 555 0102",
     "card": "5500 0000 0000 0004", "plan": "free"},
]

TOOLS = [{
    "name": "whoami",
    "description": "Returns the source IP and the Authorization header class this MCP server saw.",
    "inputSchema": {"type": "object", "properties": {}},
}]


def scrub(s):
    """Replaces each known secret in s with <REAL:name>."""
    for name, v in SECRETS.items():
        s = s.replace(v, f"<REAL:{name}>")
    return s


def classify(v):
    out = scrub(v)
    return {"len": len(v), "value": out, "real": out != v}


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *_):
        pass

    def seen(self, body=""):
        return {
            "method": self.command,
            "path": scrub(self.path),
            "src_ip": self.headers.get("CF-Connecting-IP") or self.client_address[0],
            "user_agent": self.headers.get("User-Agent"),
            "headers": {k.lower(): classify(v) for k, v in self.headers.items() if k.lower() in WATCH
                        or k.lower().startswith("x-probe")},
            "body": scrub(body[:2000]),
            "body_real": scrub(body) != body,
        }

    def record(self, body="", **extra):
        s = self.seen(body)
        line = json.dumps({"ts": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), **s, **extra})
        if LOG:
            LOG.write(line + "\n")
        print(line, flush=True)
        return s

    def send(self, code, obj=None):
        data = b"" if obj is None else json.dumps(obj, indent=2).encode() + b"\n"
        self.send_response(code)
        self.send_header("X-Hop", "origin")
        if obj is not None:
            self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def read_body(self):
        n = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(n).decode(errors="replace") if n else ""

    def do_GET(self):
        if self.path.startswith("/ws") and self.headers.get("Upgrade", "").lower() == "websocket":
            return self.websocket()
        s = self.record()
        p = self.path.split("?")[0].rstrip("/")
        if p == "/users":
            return self.send(200, USERS)
        if p.startswith("/users/"):
            u = next((u for u in USERS if str(u["id"]) == p.rsplit("/", 1)[1]), None)
            return self.send(200 if u else 404, u or {"error": "no such user"})
        if p == "/mcp":
            return self.send(405, {"error": "stateless MCP server: no SSE stream"})
        self.send(200, {"probe": "eng-880", "seen": s})

    def do_DELETE(self):
        self.record(self.read_body())
        self.send(204)

    def do_POST(self):
        raw = self.read_body()
        s = self.record(raw)
        if not self.path.startswith("/mcp"):
            return self.send(200, {"probe": "eng-880", "seen": s})
        try:
            msg = json.loads(raw)
        except json.JSONDecodeError:
            return self.send(400, {"jsonrpc": "2.0", "id": None, "error": {"code": -32700, "message": "parse error"}})
        if "id" not in msg:
            return self.send(202)
        method, mid = msg.get("method"), msg["id"]
        if method == "initialize":
            result = {"protocolVersion": msg.get("params", {}).get("protocolVersion", "2025-06-18"),
                      "capabilities": {"tools": {}}, "serverInfo": {"name": "eng880-probe", "version": "0.1.0"}}
        elif method == "ping":
            result = {}
        elif method == "tools/list":
            result = {"tools": TOOLS}
        elif method == "tools/call":
            text = json.dumps({"mcp_call_src_ip": s["src_ip"], "user_agent": s["user_agent"],
                               "authorization": s["headers"].get("authorization")})
            result = {"content": [{"type": "text", "text": text}], "isError": False}
        else:
            return self.send(200, {"jsonrpc": "2.0", "id": mid, "error": {"code": -32601, "message": method}})
        self.send(200, {"jsonrpc": "2.0", "id": mid, "result": result})

    def websocket(self):
        self.record(ws="upgrade")
        key = self.headers.get("Sec-WebSocket-Key", "")
        accept = base64.b64encode(hashlib.sha1((key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode()).digest())
        self.send_response(101)
        self.send_header("Upgrade", "websocket")
        self.send_header("Connection", "Upgrade")
        self.send_header("Sec-WebSocket-Accept", accept.decode())
        self.send_header("X-Hop", "origin")
        self.end_headers()
        self.wfile.flush()
        frames = 0
        while True:
            hdr = self.rfile.read(2)
            if len(hdr) < 2:
                break
            op, n = hdr[0] & 0x0F, hdr[1] & 0x7F
            if n == 126:
                n = struct.unpack(">H", self.rfile.read(2))[0]
            elif n == 127:
                n = struct.unpack(">Q", self.rfile.read(8))[0]
            mask = self.rfile.read(4) if hdr[1] & 0x80 else b"\0\0\0\0"
            data = bytes(b ^ mask[i % 4] for i, b in enumerate(self.rfile.read(n)))
            if op == 0x8:
                self.wfile.write(b"\x88\x00")
                break
            reply = 0xA if op == 0x9 else op
            ln = len(data)
            head = bytes([0x80 | reply]) + (bytes([ln]) if ln < 126 else b"\x7e" + struct.pack(">H", ln))
            self.wfile.write(head + data)
            self.wfile.flush()
            frames += 1
        self.record(ws="closed", frames=frames)
        self.close_connection = True


if __name__ == "__main__":
    print(json.dumps({"probe": "listening", "secrets": sorted(SECRETS)}), flush=True)
    ThreadingHTTPServer(("0.0.0.0", int(sys.argv[1]) if len(sys.argv) > 1 else 8088), Handler).serve_forever()
