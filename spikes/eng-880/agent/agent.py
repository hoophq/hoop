"""ENG-880 minimal AgentCore Runtime agent. Throwaway.

POST /invocations {"prompt": "..."}: a Bedrock Converse loop with one tool,
`shell`, which runs bash in this microVM. Returns the model's answer and
each command with its output. GET /ping answers the health check.
"""
import json
import os
import subprocess
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import boto3

MODEL = os.environ.get("MODEL_ID", "us.anthropic.claude-haiku-5-5")
REGION = os.environ.get("AWS_REGION", "us-east-1")
SYSTEM = os.environ.get("SYSTEM_PROMPT", "You are a test agent. Use the shell tool to run what the user asks, "
                        "then report each command's output verbatim.")
SESSION_HEADER = "X-Amzn-Bedrock-AgentCore-Runtime-Session-Id"
SHELL = {"toolSpec": {
    "name": "shell",
    "description": "Run one bash command. Returns stdout, stderr and the exit code.",
    "inputSchema": {"json": {"type": "object", "properties": {"command": {"type": "string"}},
                             "required": ["command"]}},
}}

bedrock = boto3.client("bedrock-runtime", region_name=REGION)


def run_shell(command, session_id):
    # c6: the session id reaches the container as a header only; the agent
    # hands it to its commands.
    env = {**os.environ, "AGENTCORE_SESSION_ID": session_id or ""}
    try:
        p = subprocess.run(["bash", "-c", command], capture_output=True, text=True, timeout=60, env=env)
        return {"exit": p.returncode, "stdout": p.stdout[-4000:], "stderr": p.stderr[-2000:]}
    except subprocess.TimeoutExpired:
        return {"exit": -1, "stdout": "", "stderr": "timeout after 60 s"}


def run_agent(prompt, session_id):
    messages = [{"role": "user", "content": [{"text": prompt}]}]
    steps = []
    for _ in range(10):
        r = bedrock.converse(modelId=MODEL, system=[{"text": SYSTEM}], messages=messages,
                             toolConfig={"tools": [SHELL]})
        msg = r["output"]["message"]
        messages.append(msg)
        uses = [c["toolUse"] for c in msg["content"] if "toolUse" in c]
        if not uses:
            answer = "".join(c.get("text", "") for c in msg["content"])
            return {"answer": answer, "steps": steps, "stop": r["stopReason"], "usage": r["usage"]}
        results = []
        for u in uses:
            out = run_shell(u["input"].get("command", ""), session_id)
            steps.append({"command": u["input"].get("command"), **out})
            results.append({"toolResult": {"toolUseId": u["toolUseId"], "content": [{"json": out}]}})
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
        self.reply(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/invocations":
            return self.reply(404, {"error": "not found"})
        n = int(self.headers.get("Content-Length") or 0)
        try:
            body = json.loads(self.rfile.read(n) or b"{}")
            session_id = self.headers.get(SESSION_HEADER)
            out = run_agent(body["prompt"], session_id)
            out["session_header"] = session_id
            out["headers_seen"] = sorted(self.headers.keys())
            self.reply(200, out)
        except Exception as e:  # a spike: return the error to the caller
            self.reply(500, {"error": f"{type(e).__name__}: {e}"})


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 8080), Handler).serve_forever()
