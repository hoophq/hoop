# MySQL lane

> hoop-inspect 0.1.0

An overlay on the stack in the parent directory: `mysql:8` behind a
`protocol: mysql` lane, beside the postgres and HTTP ones. The lane adds no
rules; it inherits the process's one guardrail rule and one mask rule and
applies them to a third protocol.

```
mysqlclient ──plaintext──> envoy :3306 ──tcp──> hoop-inspect :13306 ──TLS──> mysqldb
 mysql CLI                 tcp_proxy            mysql codec                  mysql:8
```

The standalone [`../../mysql-stack`](../../mysql-stack/README.md) is this
lane alone, without OPA and the other two protocols, and spends its single
guardrail rule on `no-destructive-sql` instead of `no-cpf-in-query`.

## Run it

```bash
./run.sh                                                                  # parent stack
docker compose -f docker-compose.yml -f mysql/docker-compose.mysql.yml up -d --wait
./mysql/demo-mysql.sh
docker compose -f docker-compose.yml -f mysql/docker-compose.mysql.yml down -v
```

Ports: `3307` on the host is Envoy's `:3306`. Inside the compose network the
client dials `envoy:3306`.

```bash
docker compose -f docker-compose.yml -f mysql/docker-compose.mysql.yml \
  exec -T mysqlclient env MYSQL_PWD=apppass \
  mysql -h envoy -P 3306 -u appuser appdb \
        -e 'SELECT name, email FROM customers'
```

The client leg is plaintext for inspection. The upstream leg is verified TLS.

## MySQL TLS split

The parent stack encrypts both legs of the postgres lane. The MySQL lane uses
the same upstream boundary but cannot use Envoy for the client leg because
MySQL negotiates TLS **in-band** after the server's greeting.

- **Client leg.** Envoy's `tcp_proxy` cannot terminate that exchange. The
  relay clears `CLIENT_SSL` from the greeting it forwards, so a default
  `--ssl-mode=PREFERRED` client continues in plaintext and remains
  inspectable. A `REQUIRED` client fails before authentication.
- **Upstream leg.** The relay reads the database greeting, sends a MySQL
  `SSLRequest`, verifies the generated CA, and authenticates inside TLS.
  `mysqldb` runs with `require_secure_transport=ON`; the demo's
  `SHOW SESSION STATUS LIKE 'Ssl_cipher'` reports the negotiated cipher.

For MySQL's `caching_sha2_password` and `sha256_password`, the relay completes
the plaintext client's RSA exchange and sends the recovered password only
inside the verified upstream TLS session. Other plugin packets pass through
with sequence numbers translated around the inserted `SSLRequest`.

## What the demo shows

1. **Masked.** `emails` is an entity rule, the process's one mask rule.
   alcatraz finds the address in each cell and the codec rebuilds the row
   around the new length. Same rule that masks the pgwire `DataRow` and the
   httpbin JSON body.
2. **Denied.** `DELETE FROM customers WHERE cpf = '111.444.777-35'` is
   refused by `no-cpf-in-query`, the process's one guardrail rule, as a real
   `ERROR 1142 (42000)`. The row count proves it stopped at the relay. A
   plain `DELETE ... WHERE id = 1` reaches the database as this file ships;
   `no-destructive-sql` is over the one-rule limit.
3. **Unsafe framing refused.** Compression is closed during negotiation with
   the reason in the relay log. A client requiring downstream TLS fails
   client-side because this inspection lane does not advertise it.

## Files

| Path | What it is |
|---|---|
| `docker-compose.mysql.yml` | the overlay: `mysqldb`, `mysqlclient`, config swaps for `hoop-inspect` and `envoy`, host port `3307` |
| `envoy-mysql.yaml` | base Envoy config plus the `:3306` `tcp_proxy` lane |
| `config-mysql.yaml` | sidecar config: `appdb`, `httpbin` and the new `mysqldb` lane |
| `seed.sql` | the `customers` fixture, MySQL dialect |
| `demo-mysql.sh` | the walk above |

The refusal reasons in the demo are printed by
`../../mysql-stack/sidecar/read-refusals.py`; the audit table by
`../sidecar/read-audit.py`, whose leak check applies unchanged because the
fixture and the masked entity are the same.
