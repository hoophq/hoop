# SQL Server 2019: login-only encryption

> hoop-inspect 0.1.0

A TDS 7.4 lane. The 2022 stack next door proves Kerberos crosses the relay;
this one proves the relay reads a session whose login it cannot read.

```
sqlclient ──TDS 7.4, login inside TLS──> hoop-inspect ──plaintext──> mssql2019
 sqlcmd -No                              inspects, denies,             appdb
                                         masks, audits
```

## Why Envoy is absent

The 2022 lane works because TDS 8.0 runs TCP, then TLS, then the protocol: an
ordinary TLS-on-connect handshake that Envoy terminates with no TDS awareness.

SQL Server 2019 has no TDS 8.0. `Encrypt=strict` needs 2022+, so the client
puts its TLS handshake inside `0x12` PRELOGIN packets and Envoy cannot speak
that. Nothing here for Envoy to terminate, so the relay takes the client's
connection directly on `11433`.

## The wire, captured

`ENCRYPT_OFF` reads as "encryption off" and means "encrypt the login only".
MS-TDS 3.2.5.3:

> the first TDS packet of the Login message MUST be encrypted using TLS/SSL
> and encapsulated in a TLS/SSL message. All other TDS packets sent or
> received MUST be in plaintext.

This server has no certificate configured and no `forceencryption`. It mints a
self-signed one at startup and encrypts the login with it anyway. Client to
relay, from a capture of this stack:

```
[    0] TDS PRELOGIN  len=88    ENCRYPTION=ENCRYPT_OFF
[   88] TDS PRELOGIN  len=340   ClientHello, wrapped in 0x12
[  428] TDS PRELOGIN  len=101   ChangeCipherSpec + Finished
[  529] RAW TLS  AppData len=450   <- LOGIN7, no TDS header at all
[  984] TDS SQLBatch  len=80       <- plaintext resumes, exactly here
[ 1064] TDS SQLBatch  len=64
[ 1128] TDS SQLBatch  len=64
```

`529 + 5 + 450 = 984`. The relay walks that region by TLS record framing and
lands on the byte where plaintext resumes. The server's reply stream carries no
raw records at all: its handshake stays inside `0x12` and its login response is
already plaintext, so the routing-ENVCHANGE guard keeps working throughout.

Before `codec/mssql/encrypted.go`, the decoder read `17 03 03 01 c2` as a
packet header, took `0x0304 = 772` for a length, and lost the connection for
good.

## Three outcomes, one server

| Client | PRELOGIN | On the wire | Relay |
|---|---|---|---|
| `sqlcmd -No -C` | `ENCRYPT_OFF` | login in TLS, statements clear | inspects |
| `sqlcmd -Nm -C` | `ENCRYPT_ON` | whole session in TLS | refuses |
| `sqlcmd -Ns` | TDS 8.0 | TLS from the first byte | refuses |

go-mssqldb sends `ENCRYPT_OFF` by default, so the first row is what an
unconfigured Go client does. ODBC Driver 18 and SqlClient 5+ default to
Mandatory, which is the second. The third is a TDS 8.0 client reaching the
relay with nothing terminating its TLS, and the error says exactly that.

Both refusals are deliberate and both are behavior changes. Those sessions
used to connect and run with no policy, no masking and no audit trail. They
now stop under `rule: stream-unsafe` with a message naming the cause.

## Server versions

The relay keys on the negotiated `ENCRYPTION` byte, never on a release, so the
table above is the whole story for any TDS 7.2+ server. Measured with the same
14 checks, `MSSQL_TAG=<tag> ./mssql2019-stack.sh && ./mssql2019-check.sh`:

| Server | TDS | Result |
|---|---|---|
| 2017-latest | 7.4 | 14/14 |
| 2019-latest | 7.4 | 14/14 |
| 2022-latest | 7.4 here, 8.0 available | 14/14 |

2022 passes through this Envoy-less topology because `-No` negotiates TDS 7.4
on it like any other release. Its TDS 8.0 mode is the `../mssql/` lane, where
Envoy terminates the client's TLS.

Untested and worth knowing:

- **SQL Server 2000 and older.** TDS 7.1 puts a 4-byte row count in DONE and
  no UserType in COLMETADATA. This codec assumes the 7.2+ layout
  (`doneTokenLen = 13`), so response masking would misparse. 2005 and up are
  fine.
- **Azure SQL Database.** Its gateway answers login with a routing ENVCHANGE
  under the default Redirect connection policy, and the codec refuses that
  outright by design. A lane pointed at Azure SQL needs the Proxy connection
  policy.
- **Windows SQL Server.** Only Linux builds were exercised. Extended
  Protection is Windows-only and set to Required it breaks this topology
  regardless of version.

## Run it

```bash
hoopinspect/scripts/dev/mssql2019-stack.sh          # build, start, seed
hoopinspect/scripts/dev/mssql2019-check.sh          # verify
hoopinspect/scripts/dev/mssql2019-stack.sh down     # tear down
```

No certificates to mint, unlike the 2022 lane: running SQL Server with no TLS
configuration is the case under test. Microsoft publishes no arm64 image, so on
Apple Silicon it runs emulated; first boot took about 100 seconds here.

A session by hand:

```bash
cd deploy/docker-compose/envoy-stack/mssql2019
docker compose -f docker-compose.mssql2019.yml exec sqlclient sqlcmd \
  -S hoop-inspect,11433 -U appuser -P 'App!Passw0rd' -No -C -d appdb \
  -Q "DELETE FROM customers WHERE id = 1"
# Msg 50000 ... destructive statements are not permitted on mssql2019
```

## Verified

`mssql2019-check.sh` reports 14 checks:

- A query crosses the relay through an encrypted login.
- Statements carry `mssql.login_encrypted`, so an operator reading the trail
  sees the window nobody could observe.
- The relay denies a `DELETE` in TDS's own error frame, and the row count
  afterwards proves the statement stopped there. `appuser` holds `db_owner`,
  so the database would have run it.
- The relay masks the response: the email by entity, the SSN by column rule,
  and an `NVARCHAR(MAX)` column through its PLP encoding.
- The relay refuses `Encrypt=Mandatory` in milliseconds under `rule:
  stream-unsafe`, and the same login succeeds straight at SQL Server, so that
  refusal belongs to the relay by design and not to 2019.
- The violation record names the rule and the statement.

## No Kerberos here

The 2022 overlay carries samba, keytabs, DNS wiring and a fixed subnet for it,
and documents that SQL Server still refuses the AD login. Leaving all of that
out keeps one variable under test and takes minutes off the boot. Use
`../mssql/` for the Kerberos story.

## The upstream hop is plaintext

Same limit as the 2022 lane. `upstream_tls` on a non-postgres lane originates a
plain TLS-on-connect handshake, which is TDS 8.0, and a 2019 server cannot
accept it at all. Closing this means teaching `proxy/starttls.go` the
`0x12`-wrapped handshake that `libhoop/agent/mssql/net.go` already implements
in `tlsHandshakeConn`.

## Files

| Path | What it is |
|---|---|
| `docker-compose.mssql2019.yml` | the stack: SQL Server 2019, the relay, a client |
| `config-mssql2019.yaml` | one lane, one guardrail, two mask rules |
| `../mssql/seed.sql` | reused: customers, the `NVARCHAR(MAX)` column, `appuser` |
| `../mssql/sqlclient/` | reused: Debian with msodbcsql18 |
