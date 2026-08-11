# MSSQL + Kerberos

> hoop-inspect 0.1.0

An overlay on the stack in the parent directory. It adds a third lane where the
client authenticates with Kerberos, using a ticket its own OS minted, and
hoop-inspect inspects each statement without holding a credential, reading the
ticket, or carrying a line of Kerberos code.

```
sqlclient ──TLS──> envoy ──plaintext──> hoop-inspect ──> mssql / appdb
 kinit alice      terminates all       inspects, denies      keytabs
                  three lanes          masks, audits
                                            │
                                        samba (AD DC)
                                      KDC + LDAP directory
```

Envoy terminates TLS on all three lanes here, so the relay reads plaintext.
That costs the parent stack's "zero Envoy extensions" claim, which is why the
contrib image lives in this overlay and not in the parent: pgwire negotiates
TLS in-band, and only `envoy.filters.network.postgres_proxy` can terminate it.

## The idea

A Kerberos service ticket names a service. The machine that answers stays out
of it.

So `mssql.hoop.test` can resolve to Envoy. The client dials that name, validates
Envoy's certificate, and asks the KDC for a ticket naming
`MSSQLSvc/mssql.hoop.test:1433`. The KDC encrypts that ticket with the SQL
Server service account's key, so Envoy and hoop-inspect carry bytes neither can
read.

hoop-inspect knows where the credential ends because TDS says so:

| Packet | Type | What the relay does |
|---|---|---|
| PRELOGIN | `0x12` | forward |
| LOGIN7 | `0x10` | forward; `DetectSSPI` records that this login is integrated |
| SSPI | `0x11` | forward verbatim, carrying the Kerberos exchange |
| Reply | `0x04` | forward, scanning for a routing redirect |
| SQLBatch / RPC | `0x01` / `0x03` | inspect: classify, evaluate policy, audit |

That boundary comes from the protocol's own message typing, so no heuristic
guesses where ciphertext stops.

## TDS 8.0 on the client leg

TDS 8.0 runs TCP, then TLS, then the protocol. The handshake is ordinary
TLS-on-connect, so Envoy terminates it with no TDS awareness, no filter, and no
extension.

TDS 7.x wraps its TLS handshake inside `0x12` PRELOGIN packets, which Envoy
cannot speak, so that lane still needs a terminator that does. The relay
handles its "encrypt the login only" mode (`ENCRYPT_OFF`) itself: MS-TDS
3.2.5.3 encrypts the first LOGIN7 packet and leaves every other packet in the
clear, and `codec/mssql/encrypted.go` walks the encrypted region by TLS record
framing to resume on the exact byte where plaintext returns. You can locate
that boundary without decrypting anything. TDS 8.0 removes the handshake Envoy
cannot terminate; it never removed a guess about where ciphertext stops.

## Connecting to the postgres lane

Two parameters are mandatory. Omit either one and the connection fails with
a message naming the cause.

```bash
psql "host=envoy port=5432 dbname=appdb user=appuser \
      sslmode=require channel_binding=disable gssencmode=disable"
```

`channel_binding=disable`. The relay strips `SCRAM-SHA-256-PLUS` from the
server's offer, because it terminated the UPSTREAM TLS and the client cannot
bind to a channel it did not see. While the client leg was plaintext that
strip was invisible. Put the client on TLS and SCRAM's own downgrade detection fires:
*"The client supports SCRAM channel binding but thinks the server does not."*

`gssencmode=disable`. libpq asks for GSS encryption first whenever a ticket is
in the cache. Envoy's postgres filter treats that request as "this session is
encrypted", enters a state it does not leave, and stops terminating anything,
the later `SSLRequest` included. Envoy's own counters show it:
`sessions_terminated_ssl: 4` beside `sessions_encrypted: 1`.

Neither is avoidable through configuration. Both are the price of Envoy owning
this lane's TLS; a relay that answered the handshake itself would need neither
the second one nor the contrib image.

## Run it

```bash
hoopinspect/scripts/dev/mssql-stack.sh          # mint certs, build, start, seed
hoopinspect/scripts/dev/mssql-kerberos-check.sh # verify
hoopinspect/scripts/dev/mssql-stack.sh down     # tear down, including volumes
```

Microsoft publishes no arm64 image for SQL Server, so on Apple Silicon it runs
emulated and first boot takes a few minutes.

A session by hand:

```bash
cd deploy/docker-compose/envoy-stack
CF=(-f docker-compose.yml -f mssql/docker-compose.mssql.yml)

docker compose "${CF[@]}" exec sqlclient bash -c \
  'echo alicepass | kinit alice@HOOP.TEST && klist'

# The guardrail, in TDS's own error frame:
docker compose "${CF[@]}" exec sqlclient sqlcmd \
  -S mssql.hoop.test,1433 -U appuser -P 'App!Passw0rd' -Ns -d appdb \
  -Q "DELETE FROM customers WHERE id = 1"
# Msg 50000 ... destructive statements are not permitted on mssqldb
```

## Verified

`mssql-kerberos-check.sh` reports these:

- A TDS 8.0 client reaches SQL Server through Envoy and the relay.
- The relay classifies T-SQL and denies a `DELETE` with the operator's message
  in a real TDS `ERROR` token. The row count afterwards proves the statement
  stopped at the relay.
- The audit trail carries the statement and the rule that fired.
- `kinit` yields a TGT, and dialling the lane yields a service ticket for
  `MSSQLSvc/mssql.hoop.test:1433`, issued for a name that resolves to Envoy.
- The relay opens a session for that login and forwards the `0x10` and `0x11`
  packets to SQL Server.
- The relay refuses a routing ENVCHANGE.
- Responses are masked: the entity rule redacts the email, the column rule
  catches the SSN that detection refuses as a fixture, and an `NVARCHAR(MAX)`
  column is masked through its PLP encoding.

## The gap: SQL Server refuses the AD login

`CREATE LOGIN [HOOP\alice] FROM WINDOWS` fails with *"Windows NT user or group
'HOOP\alice' not found"*, and an integrated login fails with error **18452**,
*"the login is from an untrusted domain"*. SQL Server's AD subsystem does not
initialize: its error log holds no Kerberos, LDAP or domain lines.

The check script demonstrates that the relay is uninvolved. `sqlhost.hoop.test`
reaches SQL Server directly, with Envoy and hoop-inspect out of the path, and
the same login fails there with the same error. The relay carried a ticket to a
server that would have refused it anyway.

Ruled out from inside the SQL Server container:

| Checked | Result |
|---|---|
| `kinit -k -t /keytabs/mssql.keytab mssqlsvc@HOOP.TEST` | TGT issued |
| `kvno ldap/dc.hoop.test` | service ticket issued |
| `ldapsearch -Y GSSAPI` for `sAMAccountName=alice` | returns alice with an objectSid |
| DNS `SRV _ldap._tcp.hoop.test` | resolves to `dc.hoop.test` |
| keytab readable by uid 10001 | yes, mode 400, owner `mssql` |
| container FQDN set to `mssql.hoop.test` | no change |

The directory answers correctly, and SQL Server does not ask it. The remaining
suspect: SQL Server on Linux may want the host joined through SSSD or winbind,
which would mean extending the official image, not configuring it.

This blocks none of what the lane demonstrates. Kerberos crosses the relay
intact, and a server that accepts AD logins would accept this one.

## The upstream hop is plaintext

The relay reaches SQL Server unencrypted inside the compose network, unlike the
`appdb` lane. A product limit causes this.

`upstream_tls` on a non-postgres lane performs a plain TLS-on-connect
handshake, which is TDS 8.0, the one TLS shape the relay can originate. SQL
Server on Linux cannot accept it: strict encryption is a Windows feature, and
`mssql-conf list` on the Linux build offers `network.forceencryption` and stops
there. Its TLS is the TDS 7.x kind, negotiated inside `0x12`
PRELOGIN packets, which `proxy/starttls.go` does not speak.

Closing this means teaching `startTLS` that handshake.
`libhoop/agent/mssql/net.go` already implements it in `tlsHandshakeConn` and is
the obvious source.

The client's leg, the one that crosses a real network, runs TDS 8.0 throughout.

## Extended Protection is not exercised here

EPA, or channel binding, is the one configuration that breaks this topology:
the client binds its authenticator to Envoy's TLS channel, and SQL Server
compares it against a channel it did not see.

This stack demonstrates neither outcome. EPA's server side is Windows-only:
Microsoft's documentation lists the EPA-capable drivers as Windows ODBC, OLE DB
and SqlClient, and the feature ships off by default even there. A Linux server
and a Linux client leave EPA unenforced.

So: no EPA, no problem, and no evidence. Against a Windows SQL Server with
Extended Protection set to **Required**, this topology fails, and a
configuration flag will not fix it. See the options study for the shape of a
fix.

## Samba, not an MIT KDC

This stack began with a 40-line MIT KDC that issued valid tickets. SQL Server
refused each one with error 18452.

The distinction is worth internalising: Kerberos proves who you are, and a
directory says who you may be. Before SQL Server accepts a principal it
resolves `DOMAIN\user` to a SID over LDAP, which no KDC can answer. Samba
provisioned as a DC serves both roles.

## Files

| Path | What it is |
|---|---|
| `docker-compose.mssql.yml` | the overlay |
| `envoy-mssql.yaml` | base Envoy config plus the `:1433` TDS 8.0 listener |
| `config-mssql.yaml` | base sidecar config plus the `mssqldb` lane |
| `samba/` | the AD domain controller: realm, accounts, SPN, keytab export |
| `sqlclient/` | Debian, msodbcsql18 and krb5; no published image carries ODBC 18 |
| `krb5.conf` | shared by the client and SQL Server |
| `mssql.conf` | SQL Server's keytab, privileged account and TLS settings |
| `seed.sql` | data plus the SQL login the data-path checks use |
| `seed-ad.sql` | the AD login, kept apart because this step fails |
| `certs/` | minted by `mssql-stack.sh`, removed by `down` |
