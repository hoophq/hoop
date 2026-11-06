"""A minimal AgentCore Runtime agent that reaches a REST API through the
hoop sidecar. It proves the Runtime path; it is not a product.

POST /invocations {"prompt": "..."} runs a Bedrock Converse loop with one
tool, fetch(method, url). GET /ping answers the health check.

The code, not the model, adds the caller token and the session id, and only
toward the sidecar: the tokens never enter the model's context. Any other
URL goes out as asked, so the model can try the API host directly and see
the call fail.
"""
import json
import os
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import boto3

MODEL = os.environ.get("MODEL_ID", "us.anthropic.claude-haiku-5-5")
SIDECAR_URL = os.environ["SIDECAR_URL"].rstrip("/")
CALLER_TOKEN = os.environ["HOOP_CALLER_TOKEN"]
API_TOKEN = os.environ.get("CUSTOMER_API_TOKEN", "")
# AgentCore sends the session id to the container in this header only.
SESSION_HEADER = "X-Amzn-Bedrock-AgentCore-Runtime-Session-Id"
SYSTEM = (f"You answer questions about our customers from the customer REST API at {SIDECAR_URL}, "
          f"for example GET {SIDECAR_URL}/users/1. Use the fetch tool. A 403 with X-Hoop-Denied is the "
          "sidecar's policy: report it, do not retry it in another form. Masked values such as "
          "[REDACTED:EMAIL_ADDRESS] are intended.")
# Cloudflare refuses urllib's default Python-urllib/3.x agent (error 1010),
# and many APIs sit behind it.
USER_AGENT = "hoop-agentcore-runtime-example/1"
FETCH = {"toolSpec": {
    "name": "fetch",
    "description": "Send one HTTP request. Returns the status and the body, or the error.",
    "inputSchema": {"json": {"type": "object", "required": ["method", "url"], "properties": {
        "method": {"type": "string"}, "url": {"type": "string"}}}},
}}

bedrock = boto3.client("bedrock-runtime")
# HTTP and HTTPS only, and no redirect: urlopen also reads file:// (the
# tokens are in /proc/self/environ), and a redirect resends every header,
# the tokens too, to the new host. A 3xx comes back as a result.
OPENER = urllib.request.OpenerDirector()
for handler in (urllib.request.HTTPHandler(), urllib.request.HTTPSHandler(), urllib.request.UnknownHandler(),
                urllib.request.HTTPDefaultErrorHandler(), urllib.request.HTTPErrorProcessor()):
    OPENER.add_handler(handler)


def fetch(method, url, session_id):
    try:
        req = urllib.request.Request(url, method=method.upper(), headers={"User-Agent": USER_AGENT})
        if url == SIDECAR_URL or url.startswith(SIDECAR_URL + "/"):
            req.add_header("X-Hoop-Token", CALLER_TOKEN)
            req.add_header("X-AgentCore-Session-Id", session_id)
            if API_TOKEN:
                req.add_header("Authorization", f"Bearer {API_TOKEN}")
        with OPENER.open(req, timeout=8) as r:
            return {"status": r.status, "body": r.read(4000).decode(errors="replace")}
    except urllib.error.HTTPError as e:
        return {"status": e.code, "denied": e.headers.get("X-Hoop-Denied"),
                "body": e.read(2000).decode(errors="replace")}
    except (urllib.error.URLError, OSError, ValueError) as e:
        return {"error": str(getattr(e, "reason", e))}


def run(prompt, session_id):
    messages = [{"role": "user", "content": [{"text": prompt}]}]
    steps = []
    for _ in range(10):
        out = bedrock.converse(modelId=MODEL, system=[{"text": SYSTEM}], messages=messages,
                               toolConfig={"tools": [FETCH]})["output"]["message"]
        messages.append(out)
        uses = [c["toolUse"] for c in out["content"] if "toolUse" in c]
        if not uses:
            return {"answer": "".join(c.get("text", "") for c in out["content"]), "steps": steps}
        results = []
        for u in uses:
            result = fetch(u["input"].get("method", "GET"), u["input"].get("url", ""), session_id)
            steps.append({**u["input"], **result})
            results.append({"toolResult": {"toolUseId": u["toolUseId"], "content": [{"json": result}]}})
        messages.append({"role": "user", "content": results})
    return {"answer": "", "steps": steps, "stop": "max_iterations"}


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def reply(self, code, obj):
        data = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path == "/ping":
            return self.reply(200, {"status": "Healthy"})
        self.reply(404, {})

    def do_POST(self):
        if self.path != "/invocations":
            return self.reply(404, {})
        try:
            body = json.loads(self.rfile.read(int(self.headers.get("Content-Length") or 0)) or b"{}")
        except ValueError:
            body = {}
        # AgentCore Runtime passes the payload as sent: refuse anything but a
        # text prompt, so a caller cannot hand the model tool calls.
        if not isinstance(body, dict) or not isinstance(body.get("prompt"), str):
            return self.reply(400, {"error": 'the body must be {"prompt": "<text>"}'})
        session_id = self.headers.get(SESSION_HEADER, "")
        try:
            self.reply(200, {"session": session_id, **run(body["prompt"], session_id)})
        except Exception as e:  # the caller sees why, not a closed connection
            self.reply(500, {"error": f"{type(e).__name__}: {e}"})


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
