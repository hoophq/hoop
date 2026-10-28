#!/usr/bin/env python3
"""Tests for evstream.py: real event stream frames in, JSON lines out.

Usage: deploy/agentcore/http/evstream_test.py
"""
import json
import os
import struct
import subprocess
import sys
import zlib

DECODER = os.path.join(os.path.dirname(os.path.abspath(__file__)), "evstream.py")


def header(name, kind, value=b""):
    """One header: name length, name, type, value. Strings and bytes carry a 2-byte length."""
    if kind in (6, 7):
        value = struct.pack(">H", len(value)) + value
    return bytes([len(name)]) + name.encode() + bytes([kind]) + value


def string(name, value):
    return header(name, 7, value.encode())


def frame(headers, payload):
    hdrs = b"".join(headers)
    prelude = struct.pack(">II", 12 + len(hdrs) + len(payload) + 4, len(hdrs))
    body = prelude + struct.pack(">I", zlib.crc32(prelude)) + hdrs + payload
    return body + struct.pack(">I", zlib.crc32(body))


def event(kind, payload):
    return frame([string(":message-type", "event"), string(":event-type", kind)], payload)


CASES = [
    ("two events in order",
     event("contentBlockDelta", b'{"delta": {"text": "hi"}}') + event("messageStop", b'{"stopReason": "end_turn"}'),
     0, [{"event": "contentBlockDelta", "delta": {"text": "hi"}},
         {"event": "messageStop", "stopReason": "end_turn"}]),
    ("an exception names its type",
     frame([string(":message-type", "exception"), string(":exception-type", "validationException")],
           b'{"message": "bad session id"}'),
     0, [{"event": "validationException", "message": "bad session id"}]),
    ("bool, int, uuid and bytes headers are skipped by size",
     frame([header("flag", 0), header("n", 4, struct.pack(">i", 7)), header("id", 9, bytes(16)),
            header("raw", 6, b"\x00\x01\x02"), string(":event-type", "chunk")], b'{"ok": true}'),
     0, [{"event": "chunk", "ok": True}]),
    ("a payload that is not JSON",
     event("chunk", b"not json"),
     0, [{"event": "chunk", "raw": "not json"}]),
    ("an empty payload",
     event("messageStart", b""),
     0, [{"event": "messageStart"}]),
    ("a JSON payload that is not an object",
     event("chunk", b"[1, 2]"),
     0, [{"event": "chunk", "body": [1, 2]}]),
    ("a truncated stream fails",
     event("chunk", b'{"a": 1}')[:-6],
     1, "truncated"),
]


def main():
    passed = failed = 0
    for name, data, want_code, want in CASES:
        run = subprocess.run([sys.executable, DECODER], input=data, capture_output=True)
        out = run.stdout.decode()
        if want_code == 0:
            got = [json.loads(line) for line in out.splitlines()] if run.returncode == 0 else None
            ok = run.returncode == 0 and got == want
        else:
            ok = run.returncode != 0 and want in run.stderr.decode()
        if ok:
            passed += 1
        else:
            failed += 1
            print(f"FAIL {name}: exit {run.returncode}\n{out}{run.stderr.decode()}")
    print(f"{passed} passed, {failed} failed")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
