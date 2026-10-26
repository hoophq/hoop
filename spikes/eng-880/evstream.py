#!/usr/bin/env python3
"""Decodes an AWS eventstream (application/vnd.amazon.eventstream) on stdin
into JSON lines: {"event": <:event-type>, ...payload}. Passes any other body
through unchanged. ENG-880, throwaway."""
import json
import struct
import sys

data = sys.stdin.buffer.read()
# An eventstream message starts with total and header lengths, then a CRC.
if len(data) < 16 or struct.unpack(">I", data[:4])[0] > len(data):
    sys.stdout.write(data.decode(errors="replace"))
    sys.exit(0)

i = 0
while i + 12 <= len(data):
    total, hlen = struct.unpack(">II", data[i:i + 8])
    h, end, headers = i + 12, i + 12 + hlen, {}
    while h < end:
        n = data[h]
        name = data[h + 1:h + 1 + n].decode()
        h += 1 + n
        t = data[h]
        h += 1
        if t == 7:  # string
            ln = struct.unpack(">H", data[h:h + 2])[0]
            headers[name] = data[h + 2:h + 2 + ln].decode()
            h += 2 + ln
        elif t == 6:  # bytes
            ln = struct.unpack(">H", data[h:h + 2])[0]
            h += 2 + ln
        else:  # bool, byte, short, int, long, timestamp, uuid: skip by size
            h += {0: 0, 1: 0, 2: 1, 3: 2, 4: 4, 5: 8, 8: 8, 9: 16}[t]
    payload = data[end:i + total - 4]
    try:
        body = json.loads(payload) if payload else {}
    except ValueError:
        body = {"raw": payload.decode(errors="replace")}
    kind = headers.get(":event-type") or headers.get(":exception-type") or headers.get(":message-type")
    print(json.dumps({"event": kind, **(body if isinstance(body, dict) else {"body": body})}))
    i += total
