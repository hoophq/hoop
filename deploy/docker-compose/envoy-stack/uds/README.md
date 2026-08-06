# Envoy → hoop-inspect over unix domain sockets

> hoop-inspect 0.1.0

An overlay on the stack in the parent directory. Same two lanes, same policy,
same masking, same TLS hop to `appdb`. The only change is what carries the
bytes between Envoy and the relay: a filesystem socket instead of a TCP port.

```
                 default                          this overlay
  envoy ──TCP :18080──> hoop-inspect    envoy ──/run/hoop-inspect/http.sock──> hoop-inspect
  envoy ──TCP :15432──> hoop-inspect    envoy ──/run/hoop-inspect/pg.sock────> hoop-inspect
```

## Run it

From the `envoy-stack` directory, after `./run.sh` has minted the certs and
built the image at least once:

```bash
docker compose -f docker-compose.yml -f uds/docker-compose.uds.yml up -d
```

Everything else works unchanged — `./demo.sh`, the admin endpoints on 19000,
the psql lane through `envoy:5432`. Tear down with the same file pair plus
`down -v`.

## Why bother

A TCP listener on 15432 is reachable by anything that can route to the pod. A
NetworkPolicy narrows that; it does not remove it. With a socket there is no
port at all, so reachability stops being a network question and becomes a
filesystem one — which is the argument for a sidecar sharing a namespace with
exactly one workload.

Check it. Ask the sidecar what it bound:

```bash
CF=(-f docker-compose.yml -f uds/docker-compose.uds.yml)   # bash: CF="-f ... -f ..."
docker compose "${CF[@]}" exec -T hoop-inspect netstat -ltn
```

```
tcp  0  0  127.0.0.11:44787  0.0.0.0:*  LISTEN     docker's internal resolver
tcp  0  0  :::19000          :::*       LISTEN     the admin API
```

That is the whole list. On the default stack the same command also shows
`:::15432` and `:::18080`; here the data lanes have no TCP presence at all.

From a peer, the same fact from the outside:

```bash
docker compose "${CF[@]}" exec -T client \
  sh -c 'nc -z -w2 hoop-inspect 15432 && echo OPEN || echo closed'   # closed
```

Use `nc`, not `(echo > /dev/tcp/host/port)`. The latter is a bash builtin; the
`client` image runs BusyBox `sh`, where it fails with no such device and calls
every port closed — including the open ones on the default stack, which makes
it a check that always passes and proves nothing.

The admin listener stays on TCP 19000 on purpose. It serves `/healthz` to a
container healthcheck and `/stats` to a scraper, and moving it to a socket
would mean exec-ing into the container to read either.

## The two permission traps

Both cost real time to diagnose, so they are worth stating plainly.

**Creating the socket.** The shared volume arrives root-owned and the relay
image runs as uid 10001, so it cannot bind:

```
listen unix /run/hoop-inspect/http.sock: bind: permission denied
```

A one-shot init container chowns the directory before either side starts. The
alternative — running the relay as root — hands a process that needs no
privileges the one thing it should never have.

**Connecting to the socket.** `connect()` on a unix socket needs **write**
permission, not read. Envoy's process runs as uid 101, not root, and `docker
exec` hands you a root shell that hides this. A socket left at the default
0755 is unreachable for uid 101, and the only symptom is:

```
[envoy] GET /json user=alice -> 503 flags=UF upstream=hoop_inspect_http
cluster.hoop_inspect_http.upstream_cx_connect_fail: 1
```

Envoy logs nothing else, and the cluster still reports `health_flags::healthy`,
because the endpoint resolved fine. The overlay solves it by running the relay
with Envoy's gid as its primary group and `umask 0002`, so every socket it
creates comes out `srwxrwxr-x` with group `envoy`. Go creates a listening
socket at `0777 &^ umask`, and the default 022 clears exactly the bit that
matters.

## Verify a run

```bash
# COMPOSE_FILE rather than a $CF variable holding "-f a -f b": that idiom
# relies on the unquoted word splitting bash does and zsh does not, so on zsh
# the whole string arrives as one argument and docker compose reports
# "no such file or directory" for a path with a space in it.
export COMPOSE_FILE=docker-compose.yml:uds/docker-compose.uds.yml

# sockets exist, group-writable, owned by the relay
docker compose exec -T envoy ls -l /run/hoop-inspect/
#  srwxrwxr-x 1 10001 envoy 0 http.sock
#  srwxrwxr-x 1 10001 envoy 0 pg.sock

# tier 1 still gates
curl -sk https://localhost:8443/json -H 'X-Hoop-User: alice' -o /dev/null -w '%{http_code}\n'   # 200
curl -sk https://localhost:8443/json -H 'X-Hoop-User: bob'   -o /dev/null -w '%{http_code}\n'   # 403

# masking and the guardrail, over the socket
pg() {
  docker compose exec -T client env PGPASSWORD=apppass PGSSLMODE=disable \
    psql -h envoy -p 5432 -U appuser -d appdb "$@"
}
pg -c 'SELECT name, email, ssn FROM customers;'   # redacted email, masked ssn
pg -c 'DELETE FROM customers WHERE id=1;'         # FATAL: destructive statements ...

# the TLS hop to appdb is unaffected
pg -c 'SELECT ssl, version FROM pg_stat_ssl WHERE pid=pg_backend_pid();'   # t | TLSv1.3
```

`COMPOSE_FILE` applies to every `docker compose` in the shell, `./demo.sh`
included, so the overlay stays selected without repeating the flags.

## Why the default stack stays on TCP

Sockets mean both containers mount a shared volume and agree on uids. That is
a fine trade in a real deployment, where a sidecar and its workload already
share a pod spec. It is a poor way to open a README, because it buries the
claim the stack exists to make: hoop-inspect is an ordinary upstream needing
no Envoy extension. TCP shows that in fewer moving parts, and this overlay is
here for when you want the tighter boundary.

## Kubernetes

The same shape, with an `emptyDir` in place of the named volume:

```yaml
volumes:
  - name: inspect-sockets
    emptyDir: {}
containers:
  - name: hoop-inspect
    securityContext: { runAsUser: 10001, runAsGroup: 101 }
    volumeMounts: [{ name: inspect-sockets, mountPath: /run/hoop-inspect }]
  - name: envoy
    volumeMounts: [{ name: inspect-sockets, mountPath: /run/hoop-inspect }]
```

`fsGroup` on the pod securityContext replaces the init container: the kubelet
applies it to the `emptyDir` before any container starts. Set it to Envoy's
gid and both sides can use the directory without a chown step.
