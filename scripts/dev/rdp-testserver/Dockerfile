# A disposable RDP server for exercising the agent's RDP plumbing locally and
# in CI. No Windows licence, no GPU, no LAN dependency.
#
# WHY NOT xrdp: the agent requests PROTOCOL_HYBRID | PROTOCOL_HYBRID_EX and
# deliberately does not set PROTOCOL_SSL, so it refuses to downgrade NLA to
# plain TLS (agentrs/src/rdp_proxy.rs). xrdp has no server-side NLA: asked for
# HYBRID|HYBRID_EX it selects PROTOCOL_RDP (0x00000000), and the agent then
# fails with "server selected security protocol ..., which is not supported
# for credential injection". Measured against xrdp 0.9.24.
#
# FreeRDP's shadow server does support NLA. Asked for HYBRID|HYBRID_EX it
# selects PROTOCOL_HYBRID (0x00000002), which the agent accepts, and a full
# X.224 -> TLS -> CredSSP -> capability-exchange connection completes.
#
# CAVEAT: this validates the plumbing, not Windows. FreeRDP's NLA is not
# Microsoft's, so Windows-specific CredSSP behavior is out of scope here.
FROM ubuntu:24.04

RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        xvfb \
        x11-apps \
        xterm \
        openssl \
        ca-certificates \
        procps \
        passwd \
        adduser \
        freerdp2-shadow-x11 \
    && rm -rf /var/lib/apt/lists/*

RUN adduser --disabled-password --gecos "" hooptest \
    && echo 'hooptest:hooptest' | chpasswd \
    && mkdir -p /etc/winpr

COPY start.sh /start.sh
RUN chmod +x /start.sh

EXPOSE 3389
CMD ["/start.sh"]
