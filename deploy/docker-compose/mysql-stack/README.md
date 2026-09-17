# Envoy + hoop-inspect, MySQL lane

> hoop-inspect 0.1.0

The [`envoy-stack`](../envoy-stack/README.md) shape with MySQL as the upstream:
Envoy owns the network path, hoop-inspect is an ordinary upstream behind it
that decodes the MySQL protocol, enforces policy per statement, writes an audit
trail and masks result sets.

```
mysql CLI ──plaintext──> envoy ──tcp──> hoop-inspect :13306 ──TLS──> appdb
                        tcp_proxy      mysql codec              mysql:8
                                       inspects, masks, audits
```

There is no hoop gateway and no hoop agent here. No OPA either: Envoy has no
MySQL parser in the main image, so `ext_authz` would have nothing to authorize
on. Everything interesting about this connection is invisible to Envoy and
visible to hoop-inspect.

## Run it

```bash
./run.sh              # build the sidecar image if missing, compose up
./demo.sh             # walk the lane and print the audit trail
./run.sh --rebuild    # rebuild the sidecar image, then up
./run.sh down         # tear down, including volumes
```

Needs `docker`, `curl`, `python3`. No local Go: the sidecar image is the one
`../envoy-stack` builds, from `../envoy-stack/sidecar/Dockerfile`, so a
`hoop-inspect:local` built by either stack serves both. It needs `./libhoop`
beside the repo, the same constraint as building `sidecar/` at all.

Ports: `3307` mysql, `19000` sidecar admin, `9901` Envoy admin. Inside the
compose network the listener is `envoy:3306`; 3307 is only the host-side
publication.

## Connecting

```bash
docker compose exec -T client env MYSQL_PWD=apppass \
  mysql -h envoy -P 3306 -u appuser appdb \
        --ssl-mode=DISABLED \
        --server-public-key-path=/etc/mysql/relay-certs/relay-auth.pub \
        -e 'SELECT name, email FROM customers'
```

The client pins the relay's public key and sends an RSA-encrypted password
without requesting a key. hoop-inspect decrypts it, then sends the password
through verified TLS to a MySQL server that refuses plaintext connections.

## MySQL TLS split

MySQL negotiates TLS **in-band**: the server greets first, then a client that
wants encryption sends a truncated handshake response (an `SSLRequest`) and
begins a TLS handshake on the same socket.

- **Client leg.** Envoy's `tcp_proxy` cannot terminate that exchange, and the
  relay must see plaintext MySQL frames to enforce policy. It clears
  `CLIENT_SSL` in the greeting forwarded to the client. A default
  `--ssl-mode=PREFERRED` client continues in plaintext; `REQUIRED` fails
  before authentication instead of creating an opaque session.
- **Upstream leg.** The relay reads the database greeting, sends its own
  `SSLRequest`, verifies the server against the generated CA, and completes
  authentication inside TLS. `appdb` runs with `require_secure_transport=ON`.
  `SHOW SESSION STATUS LIKE 'Ssl_cipher'` in `./demo.sh` proves this hop has a
  negotiated cipher.

`caching_sha2_password` and `sha256_password` encrypt passwords on a plaintext
connection. A client can request a key or load a trusted key before it
connects. This stack tests the second path:

1. `mysqlcerts` creates `relay-auth.key` and `relay-auth.pub`.
2. hoop-inspect reads the private key through `mysql_auth_key_file`.
3. The client reads the public key through `--server-public-key-path` and sends
   ciphertext without a public-key request.

The relay decrypts that response and sends the recovered NUL-terminated
password inside the verified upstream TLS session. The backend server's public
key cannot replace `relay-auth.pub` because hoop-inspect does not hold the
backend private key.

## What the demo shows
1. **Pinned-key authentication.** The first `appuser` connection sends a
   direct RSA response encrypted for the relay key. The database health check
   uses `root`, so it does not warm `appuser`'s `caching_sha2_password` cache.

2. **Masked result set.** The codec keys masking on the column NAME from the
   definitions the server sends ahead of every result set, so
   `columns: [email]` is exact and covers both row encodings. Rows are
   rebuilt around the new values: every cell and the packet holding it are
   length-prefixed, so a substituted byte string would desynchronize the
   client.
3. **Denied DELETE, as a native error.** The client reads
   `ERROR 1142 (42000): destructive statements are not permitted on appdb`,
   the frame the server itself sends for a privilege refusal, and the row
   count proves the statement stopped at the relay.
4. **Executable comment.** `/*! DROP TABLE customers */` is a drop to MySQL
   and to the lexer, so the same rule refuses it.
5. **Unsafe framing refused.** Compression is closed at the handshake with a
   reason in the relay log. A client requiring TLS also fails, but earlier:
   the client-facing greeting deliberately does not advertise `CLIENT_SSL`;
   the independently verified upstream hop remains encrypted.

The mysql CLI, since 8.0.23, negotiates `CLIENT_QUERY_ATTRIBUTES` and
prefixes every `COM_QUERY` with an attribute block that `go-sql-driver` never
sends. The codec strips it; a decoder that misses it hands the classifier
`\x00\x01DROP TABLE ...`, finds no verb, and forwards. That is why the client
here is the real CLI and not a driver.

## What the codec refuses

All return `ErrStreamUnsafe`; the gate denies regardless of policy and the
relay closes the session.

| Negotiation | Where | Client-side fix named in the message |
|---|---|---|
| `CLIENT_COMPRESS` / `CLIENT_ZSTD_COMPRESSION_ALGORITHM` | first command after the handshake | `--compression-algorithms=uncompressed` |
| `CLIENT_SSL` upgrade | handshake response | `--ssl-mode=DISABLED`, or terminate TLS ahead of the relay |
| `CLIENT_OPTIONAL_RESULTSET_METADATA` | first command after the handshake | `resultset_metadata=FULL` |
| `LOAD DATA LOCAL INFILE` request from the server | reply to the statement | `--local-infile=0` |
| `COM_STMT_FETCH` on a cursor the relay did not see open | the fetch's reply | reconnect through the relay; cursors opened before it cannot be masked |
| `COM_STATISTICS`, `COM_FIELD_LIST`, `COM_CHANGE_USER`, replication commands | the command | none; administrative, not a data path |

`libhoop/v2/codec/mysql` carries the reasoning per refusal in `unsafe.go`,
`mysql.go` (`checkNegotiated`) and `client.go` (`decodeClient`).

## Files

| Path | What it is |
|---|---|
| `docker-compose.yml` | appdb (mysql:8), client (mysql:8 CLI), hoop-inspect, envoy |
| `envoy/envoy.yaml` | one `tcp_proxy` listener, `:3306` to `hoop-inspect:13306` |
| `sidecar/config.yaml` | the `appdb` lane: one guardrail rule, one column mask rule |
| `sidecar/read-refusals.py` | prints the reason behind each session the relay refused |
| `upstream/seed.sql` | the `customers` fixture, MySQL dialect |
| `run.sh`, `demo.sh` | bring-up and the walk above |

The audit table in the demo is rendered by
`../envoy-stack/sidecar/read-audit.py`; the fixture and the masked column are
the same, so its leak check applies unchanged.
