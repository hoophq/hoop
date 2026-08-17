# A workstation that can speak TDS 8.0 and hold a Kerberos ticket.
#
# Microsoft publishes mcr.microsoft.com/mssql-tools, but that image carries
# ODBC 17, which predates TDS 8.0 and rejects `-N strict` outright. There is
# no published mssql-tools18 image, so the driver gets installed here.
#
# msodbcsql18 is what makes the whole lane work from the client side:
#   * Encrypt=strict, so the client speaks TDS 8.0 and Envoy can terminate an
#     ordinary TLS handshake instead of a 0x12-wrapped one.
#   * Kerberos via the system GSSAPI, so `kinit` then `sqlcmd -E` uses the
#     ticket the OS already holds. Nothing in hoop mints or reads it.
FROM debian:bookworm-slim

ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update -qq \
    && apt-get install -y -qq --no-install-recommends \
        curl gnupg ca-certificates apt-transport-https \
        krb5-user libgssapi-krb5-2 \
    && install -d -m 0755 /etc/apt/keyrings \
    && curl -fsSL https://packages.microsoft.com/keys/microsoft.asc \
        | gpg --dearmor -o /etc/apt/keyrings/microsoft.gpg \
    && echo "deb [arch=amd64,arm64 signed-by=/etc/apt/keyrings/microsoft.gpg] https://packages.microsoft.com/debian/12/prod bookworm main" \
        > /etc/apt/sources.list.d/mssql-release.list \
    && apt-get update -qq \
    && ACCEPT_EULA=Y apt-get install -y -qq --no-install-recommends \
        msodbcsql18 mssql-tools18 \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# krb5-user drops a stub krb5.conf; the compose file bind-mounts the real one
# over it. Keeping PATH pointed at the tools means demo commands read as
# `sqlcmd ...` rather than a full path.
ENV PATH="/opt/mssql-tools18/bin:${PATH}"

CMD ["sleep", "infinity"]
