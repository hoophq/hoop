#!/usr/bin/env python3
"""Decodes an AWS event stream (application/vnd.amazon.eventstream) on stdin
into JSON lines: {"event": <:event-type>, ...payload}. InvokeHarness answers
in this binary format, so jq alone cannot read it. Stdlib only."""
import json
import struct
import sys

# Header value sizes by type, for the types this decoder skips.
FIXED = {0: 0, 1: 0, 2: 1, 3: 2, 4: 4, 5: 8, 8: 8, 9: 16}

data = sys.stdin.buffer.read()
i = 0
while i + 12 <= len(data):
    # A message: total length, header length, prelude CRC, headers, payload, CRC.
    total, hlen = struct.unpack(">II", data[i:i + 8])
    if total < 16 or i + total > len(data):
        sys.exit(f"evstream: truncated message at byte {i}")
    h, end, headers = i + 12, i + 12 + hlen, {}
    while h < end:
        n = data[h]
        name = data[h + 1:h + 1 + n].decode()
        kind = data[h + 1 + n]
        h += 2 + n
        if kind in (6, 7):  # bytes, string: 2-byte length
            ln = struct.unpack(">H", data[h:h + 2])[0]
            if kind == 7:
                headers[name] = data[h + 2:h + 2 + ln].decode()
            h += 2 + ln
        else:
            h += FIXED[kind]
    payload = data[end:i + total - 4]
    try:
        body = json.loads(payload) if payload else {}
    except ValueError:
        body = {"raw": payload.decode(errors="replace")}
    event = headers.get(":event-type") or headers.get(":exception-type") or headers.get(":message-type")
    print(json.dumps({"event": event, **(body if isinstance(body, dict) else {"body": body})}))
    i += total
