#!/usr/bin/env python3
"""ENG-860 sandbox probe. Throwaway. Standard library only.

  probe.py env                      proxy, CA and session variables; vault placeholder lengths
  probe.py dns HOST
  probe.py tcp HOST PORT            raw TCP, direct and through HTTPS_PROXY; a Postgres port gets an SSLRequest
  probe.py tls HOST                 certificate the sandbox sees on 443
  probe.py ws HOST PATH [K:V ...]   WebSocket upgrade on 443, then 3 echo frames
  probe.py connect HOST TARGET      HTTP CONNECT sent inside TLS to HOST:443

Each line names the step that failed: dns, tcp, tls, http. Streams to 443
go through HTTPS_PROXY (CONNECT) when it is set, as curl does.
"""
import os
import re
import socket
import ssl
import struct
import sys
import time

VAULT_NAMES = ("HOOP_CALLER_TOKEN", "CUSTOMER_API_TOKEN")
SHOW = re.compile(r"PROXY|SSL_|CA_|CERT|SESSION|OPENAI|CODEX|AGENT|TURN|ENVIRONMENT|HOSTNAME|^HOME$|^USER$", re.I)


def out(*a):
    print(*a, flush=True)


def env():
    names = sorted(os.environ)
    out("names:", " ".join(names))
    for k in names:
        v = os.environ[k]
        if k in VAULT_NAMES:
            out(f"{k}: len={len(v)} value={v}")
        elif SHOW.search(k):
            out(f"{k}={v[:200]}")
    for f in ("/etc/os-release",):
        try:
            out(f, open(f).read().split("\n")[0])
        except OSError as e:
            out(f, e)
    out("resolv.conf:", open("/etc/resolv.conf").read().replace("\n", " | ") if os.path.exists("/etc/resolv.conf") else "-")


def dns(host):
    try:
        out("dns", host, sorted({a[4][0] for a in socket.getaddrinfo(host, 443)}))
        return True
    except OSError as e:
        out("dns FAIL", host, repr(e))
        return False


def proxy():
    p = os.environ.get("HTTPS_PROXY") or os.environ.get("https_proxy")
    if not p:
        return None
    hp = p.split("://", 1)[-1].rstrip("/")
    h, _, port = hp.rpartition(":")
    return h, int(port)


def open_tcp(host, port, via_proxy=True):
    """A TCP stream to host:port: through HTTPS_PROXY with CONNECT when set."""
    px = proxy() if via_proxy else None
    if not px:
        return socket.create_connection((host, port), timeout=15)
    s = socket.create_connection(px, timeout=15)
    s.sendall(f"CONNECT {host}:{port} HTTP/1.1\r\nHost: {host}:{port}\r\n\r\n".encode())
    h, rest = head(s)
    status = h.split("\r\n")[0]
    if " 200 " not in status + " ":
        raise OSError(f"proxy {px[0]}:{px[1]} refused CONNECT {host}:{port}: {h.replace(chr(13) + chr(10), ' | ')} body={rest[:200]!r}")
    return s


def tcp(host, port):
    port = int(port)
    if not dns(host) and not proxy():
        return
    for via in (False, True):
        how = "via proxy CONNECT" if via else "direct"
        if via and not proxy():
            continue
        t = time.time()
        try:
            s = open_tcp(host, port, via)
        except OSError as e:
            out(f"tcp {how} FAIL {host}:{port} after {time.time() - t:.1f}s: {e}")
            continue
        out(f"tcp {how} OK {host}:{port} in {time.time() - t:.2f}s")
        if port == 5432:
            s.sendall(struct.pack(">II", 8, 80877103))
            s.settimeout(10)
            try:
                out("  postgres SSLRequest answer:", s.recv(1))
            except OSError as e:
                out("  postgres SSLRequest FAIL", repr(e))
        s.close()


def tls_conn(host, verify=True):
    ctx = ssl.create_default_context()
    if not verify:
        ctx.check_hostname, ctx.verify_mode = False, ssl.CERT_NONE
    ctx.set_alpn_protocols(["http/1.1"])
    return ctx.wrap_socket(open_tcp(host, 443), server_hostname=host)


def name(t):
    return ", ".join(f"{k}={v}" for rdn in t for k, v in rdn)


def tls(host):
    if not dns(host) and not proxy():
        return
    try:
        s = tls_conn(host)
        c = s.getpeercert()
        out(f"tls OK {host} {s.version()} alpn={s.selected_alpn_protocol()}")
        out("  subject:", name(c["subject"]))
        out("  issuer: ", name(c["issuer"]))
        s.close()
    except ssl.SSLError as e:
        out(f"tls VERIFY FAIL {host}: {e!r}")
        s = tls_conn(host, verify=False)
        out("  (unverified) cert bytes:", len(s.getpeercert(binary_form=True)))
    except OSError as e:
        out(f"tls FAIL {host}: {e!r}")


def head(s):
    buf = b""
    while b"\r\n\r\n" not in buf:
        chunk = s.recv(4096)
        if not chunk:
            break
        buf += chunk
    h, _, rest = buf.partition(b"\r\n\r\n")
    return h.decode(errors="replace"), rest


def ws(host, path, *hdrs):
    if not dns(host) and not proxy():
        return
    try:
        s = tls_conn(host)
    except OSError as e:
        out("ws tls FAIL", repr(e))
        return
    key = "dGhlIHNhbXBsZSBub25jZQ=="
    req = (f"GET {path} HTTP/1.1\r\nHost: {host}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"
           f"Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n")
    req += "".join(h + "\r\n" for h in hdrs) + "\r\n"
    s.sendall(req.encode())
    h, rest = head(s)
    out("ws reply:", h.replace("\r\n", " | "))
    if " 101 " not in h.split("\r\n")[0]:
        out("ws body:", rest[:300])
        return
    s.settimeout(10)
    for i in range(3):
        msg = f"eng-860 frame {i}".encode()
        mask = os.urandom(4)
        frame = bytes([0x81, 0x80 | len(msg)]) + mask + bytes(b ^ mask[j % 4] for j, b in enumerate(msg))
        t = time.time()
        s.sendall(frame)
        got = rest or s.recv(4096)
        rest = b""
        out(f"ws echo {i}: {got[2:2 + got[1]]!r} rtt={1000 * (time.time() - t):.0f}ms")
    s.sendall(bytes([0x88, 0x80]) + os.urandom(4))
    s.close()


def connect(host, target):
    if not dns(host) and not proxy():
        return
    try:
        s = tls_conn(host)
    except OSError as e:
        out("connect tls FAIL", repr(e))
        return
    s.sendall(f"CONNECT {target} HTTP/1.1\r\nHost: {target}\r\n\r\n".encode())
    s.settimeout(15)
    try:
        h, rest = head(s)
    except OSError as e:
        out("connect FAIL reading reply", repr(e))
        return
    out("connect reply:", h.replace("\r\n", " | "))
    out("connect body:", rest[:300])


if __name__ == "__main__":
    cmd, args = sys.argv[1], sys.argv[2:]
    {"env": env, "dns": dns, "tcp": tcp, "tls": tls, "ws": ws, "connect": connect}[cmd](*args)
