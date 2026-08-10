# Postgres + Kerberos

> hoop-inspect 0.1.0

An overlay on the stack in the parent directory. A client authenticates with a
Kerberos ticket its own OS minted, and hoop-inspect inspects, masks and audits
each statement without holding a credential or reading the ticket.

```
psql ──TLS──> envoy ──plaintext──> hoop-inspect ──TLS 1.3──> appdb
 kinit alice  terminates          inspects, masks, audits     keytab
                                        │
                                    samba (AD DC)
                                  KDC + LDAP directory
```

The sibling `mssql/` overlay runs the same realm against SQL Server. This one
exists because SQL Server publishes no arm64 image, so on Apple Silicon it runs
emulated and adds minutes to each iteration. This stack starts in seconds, and
it is the lane where a Kerberos login succeeds end to end.

## Run it

```bash
hoopinspect/scripts/dev/pg-stack.sh           # build, start, wait
hoopinspect/scripts/dev/pg-kerberos-check.sh  # verify
hoopinspect/scripts/dev/pg-stack.sh down      # tear down, including volumes
```

Two shortcuts:

```bash
pg-stack.sh psql  -c 'SELECT * FROM customers'   # password session
pg-stack.sh kinit -c 'SELECT current_user'       # Kerberos session
```

## The idea

A Kerberos service ticket names a service. The machine that answers stays out
of it.

So `appdb.hoop.test` resolves to **Envoy**. The client dials that name and asks
the KDC for a ticket naming `postgres/appdb.hoop.test`, which the KDC encrypts
with the database's key. Envoy and hoop-inspect carry bytes neither can read.

Note the SPN shape against MSSQL's: libpq builds `krbsrvname/host` with no port,
where a SQL client builds `MSSQLSvc/host:port`.

## GSS encryption is refused

This is the part worth understanding, because it fails silently when it goes
wrong.

libpq defaults `gssencmode=prefer`, so a client holding a ticket asks to wrap
the whole session in GSSAPI before anything else, ahead of TLS. Accept it and
each later byte is ciphertext: no statements, no masking, no audit trail, and
no error saying inspection stopped.

The relay answers `N`. The client falls back and **keeps its Kerberos
authentication**, which travels as ordinary tagged messages the codec forwards
untouched. Postgres agrees:

```
gss_authenticated | encrypted
       true       |   false
```

Authentication by ticket, transport left readable.

## Connecting

Two parameters are mandatory on this lane, Omit either one and the connection fails with a
message naming the cause.

```bash
psql "host=appdb.hoop.test port=5432 dbname=appdb user=alice@HOOP.TEST \
      sslmode=require channel_binding=disable gssencmode=disable"
```

`channel_binding=disable`. The relay strips `SCRAM-SHA-256-PLUS` from the
server's offer, because it terminated the UPSTREAM TLS and the client cannot
bind to a channel it did not see. While the client leg was plaintext that strip
was invisible. Put the client on TLS and SCRAM's own downgrade detection fires.

`gssencmode=disable`. Envoy's postgres filter treats a `GSSENCRequest` as "this
session is encrypted", enters a state it does not leave, and stops terminating
anything, the later `SSLRequest` included. The relay would answer that request
correctly, and Envoy sees the bytes first.

Both come from Envoy owning this lane's TLS. A relay that answered the
handshake itself would need neither, which is the purpose of `downstream_tls`
in `proxy/downstream.go`.

## Verified

`pg-kerberos-check.sh` reports 15 checks, including:

- Envoy terminates the lane's TLS, confirmed against its own
  `sessions_terminated_ssl` counter.
- `kinit` yields a TGT, and dialling the lane yields a service ticket for
  `postgres/appdb.hoop.test`.
- `gss_authenticated=true, encrypted=false`.
- A **default-configured** Kerberos client connects, and its statement reaches
  the audit trail. That is the regression guard for the GSS refusal: before it,
  the same client left no trace at all.
- A `DELETE` is denied with the operator's message, and the row count proves it
  stopped at the relay.
- Email redacted by the entity rule, SSN masked by the column rule.
- Sessions carry `principal=alice@HOOP.TEST`, and the violation event names
  both the rule and the principal.

## Files

| Path | What it is |
|---|---|
| `docker-compose.postgres.yml` | the overlay |
| `envoy-postgres.yaml` | base Envoy config plus TLS termination on the pg lane |
| `config-postgres.yaml` | sidecar config: the `appdb` and `httpbin` lanes |
| `pg_hba.conf` | `scram-sha-256` for `appuser`, `gss` for everyone else |
| `pg-seed-kerberos.sql` | the role a Kerberos principal maps onto |

The realm itself lives in `../kerberos/`, shared with the `mssql/` overlay so
the accounts and SPNs are defined once.

## Why Postgres authorizes a Kerberos login and SQL Server does not

Kerberos proves who you are. Something still has to say who you may be.

Postgres compares the authenticated principal against `pg_authid`, a local
table, so a keytab and a `CREATE ROLE` are enough. SQL Server resolves
`DOMAIN\user` to a SID over LDAP before it will store a login, which is why the
`mssql/` overlay runs a domain controller and still stops at error 18452.

Same relay, same ticket, different authorization model.
