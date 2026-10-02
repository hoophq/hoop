# Oracle lane

> hoop-inspect 0.1.0

An overlay on the stack in the parent directory: Oracle Database Free behind a
`protocol: oracle` lane, beside the postgres and HTTP ones. The lane adds no
rules; it inherits the process's one guardrail rule and one mask rule and
applies them to a third protocol, for a thick and a thin client.

```
SQL*Plus (OCI)  ──┐
                  ├─TNS─> envoy :1521 ──tcp──> hoop-inspect :11521 ──TNS──> oracledb
python-oracledb ──┘       tcp_proxy            oracle codec                 Oracle Free 23
 (thin)
```

## Run it

```bash
./run.sh                                                                    # parent stack
docker compose -f docker-compose.yml -f oracle/docker-compose.oracle.yml up -d --wait
./oracle/demo-oracle.sh                                                     # exits 1 on any failed check
docker compose -f docker-compose.yml -f oracle/docker-compose.oracle.yml down -v
```

After a codec change in `../../../libhoop` or `../../../sidecar`, rebuild the
relay first: `./run.sh --rebuild`, or `docker compose ... build hoop-inspect`.

### Another Oracle version

Three variables select the server and the SQL*Plus client; set them for both
`up` and the demo. Oracle XE 21c with its own SQL*Plus 21, and with SQL*Plus
23 from the Free image:

```bash
export ORACLE_IMAGE=gvenzl/oracle-xe:21-slim-faststart ORACLE_SERVICE=XEPDB1
docker compose -f docker-compose.yml -f oracle/docker-compose.oracle.yml up -d --wait
./oracle/demo-oracle.sh

export ORACLE_CLIENT_IMAGE=gvenzl/oracle-free:23-slim-faststart
docker compose -f docker-compose.yml -f oracle/docker-compose.oracle.yml up -d --wait oracleclient
./oracle/demo-oracle.sh
```

The XE 21c image is amd64 only; on Apple silicon it runs under emulation and
starts in about 20 seconds. No public image exists for 19c.

Ports: `1522` on the host is Envoy's `:1521`. Inside the compose network the
clients dial `envoy:1521/FREEPDB1` (`XEPDB1` on 21c) as `appuser`/`apppass`.

```bash
docker compose -f docker-compose.yml -f oracle/docker-compose.oracle.yml \
  exec -T oracleclient sqlplus -S -L appuser/apppass@//envoy:1521/FREEPDB1 <<'EOF'
SELECT name, email FROM customers;
EOF
```

## Transport

Both legs are plaintext TNS. The oracle codec inspects plaintext TNS/TTC only:
it has no `upstream_tls`, and it refuses a session that negotiates Oracle
native encryption or integrity (ANO) rather than forwarding what it cannot
read. TCPS is not supported on either leg.

## What the demo shows

1. **Masked.** `emails` is an entity rule, the process's one mask rule.
   alcatraz finds the address in each cell and the codec re-encodes the row
   around the new length, for SQL*Plus and for python-oracledb, including a
   bind-variable lookup.
2. **Denied.** `DELETE FROM customers WHERE cpf = '111.444.777-35'` is refused
   by `no-cpf-in-query` with a native `ORA-01031` carrying the operator's
   message, then the relay closes that session. The row count, read directly
   from `oracledb`, proves it stopped at the relay.
3. **Recovered.** A new session after the denials works.
4. **Audited.** Every statement, verdict and mask event is in the audit trail,
   and no masked value appears in it.

## Known refusal: row images with unselected columns

Oracle can return a row as a raw block row piece (TTC row header `0x1a`). That
piece carries every stored column of the table row, not only the SELECT list:
`SELECT note FROM t WHERE id = :k` on `t(id, email, note)` also sends `id` and
`email`. An unselected value has no name, type or charset a mask rule could
match, so with masking on the relay closes the session instead of forwarding
it; the relay logs `row image carries undescribed slot N`. Selecting every
column, or a lane with no mask rules, is unaffected. The captured cases were
single-row tables; the customers fixture does not trigger it.

## Files

| Path | What it is |
|---|---|
| `docker-compose.oracle.yml` | the overlay: `oracledb`, `oracleclient`, `oraclethin`, config swaps for `hoop-inspect` and `envoy`, host port `1522` |
| `envoy-oracle.yaml` | base Envoy config plus the `:1521` `tcp_proxy` lane |
| `config-oracle.yaml` | sidecar config: `appdb`, `httpbin` and the new `oracledb` lane |
| `seed.sh` | the `customers` fixture, Oracle dialect, created in `ORACLE_SERVICE` |
| `thin.py` | the python-oracledb (thin) client the demo drives |
| `demo-oracle.sh` | the walk above |
