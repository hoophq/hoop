// A minimal stdio MCP server, standing in for the one a developer would run
// on their own laptop. Newline-delimited JSON-RPC on stdin/stdout.
//
// It prints a banner to stdout first, on purpose: real npx-launched servers
// do, and the client pump must skip non-JSON lines rather than forward them.
process.stdout.write("fake-mcp starting up\n");

let buf = "";
process.stdin.on("data", (chunk) => {
  buf += chunk;
  let i;
  while ((i = buf.indexOf("\n")) >= 0) {
    const line = buf.slice(0, i).trim();
    buf = buf.slice(i + 1);
    if (!line) continue;
    let req;
    try { req = JSON.parse(line); } catch { continue; }
    if (req.id === undefined || req.id === null) continue; // notification

    let result;
    if (req.method === "initialize") {
      result = {
        protocolVersion: "2025-11-25",
        capabilities: { tools: {} },
        serverInfo: { name: "fake-mcp-on-laptop", version: "1.0.0" },
      };
    } else if (req.method === "tools/list") {
      result = {
        tools: [
          { name: "whoami", description: "who is running me",
            inputSchema: { type: "object", properties: {} } },
          { name: "delete_everything", description: "dangerous",
            inputSchema: { type: "object", properties: {} } },
        ],
      };
    } else if (req.method === "tools/call") {
      // Report the pid so the test can prove the process is local.
      result = {
        content: [{ type: "text",
          text: `ran on pid ${process.pid} as ${process.env.WHOAMI || "unknown"}` }],
      };
    } else {
      result = {};
    }
    process.stdout.write(JSON.stringify({ jsonrpc: "2.0", id: req.id, result }) + "\n");
  }
});
