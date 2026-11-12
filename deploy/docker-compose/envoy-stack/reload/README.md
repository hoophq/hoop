# Adding and removing listeners without a restart

The sidecar re-reads its config while it runs. A rule edit swaps rules in
place; a listener added to the file starts in place, a listener removed
from it stops in place. The process does not restart, and every connection
on the other lanes stays open. This overlay shows that on the base stack.

```bash
./reload/run.sh          # the base stack, with the sidecar reading reload/work/
./reload/demo.sh         # add a lane, query it, refuse a bind conflict, remove it
./reload/run.sh down     # tear down, including the working copy
```

`demo.sh` exits non-zero if any check fails. Its steps:

| Step | Edit to `reload/work/config.yaml` | The sidecar logs | Proven by |
|---|---|---|---|
| session | none; a `psql` runs `pg_sleep(20)` through Envoy on `appdb` | | the row arrives at the end |
| add | a fourth lane `appdb-live` on `:15434` | `listener started` and `configuration applied … added=1` | `/stats` lists it; a `SELECT` through it returns a masked email |
| refuse | a fifth lane on `:19000`, the admin port | `added a listener this process could not start; keeping the running lanes` with `bind: address already in use` | `/stats` still lists four; the document is refused as a whole |
| remove | `appdb-live` deleted | `listener stopped` and `configuration applied … removed=1` | `/stats` lists three; `:15434` refuses |

The demo sends `SIGHUP` after each edit so it does not wait for the
ten-second stat tick. Under a control plane the heartbeat delivers the same
document and reaches the same outcome; the plane's sidecar page shows
`refused` with the reason for the bind conflict.

## The directory mount

The base stack binds `sidecar/config.yaml` into the container as one file,
and Docker binds a file by inode. Most editors and `sed -i` write a new file
and rename it over the old one, so the container keeps reading the old inode
and does not see the edit. The overlay binds the directory `reload/work/`
instead and points `-config` into it; `run.sh` seeds it with a copy of the
base config. A Kubernetes ConfigMap volume is a directory and an atomic
symlink swap, which the sidecar's stat sees, so this is only a compose
concern.

On macOS a bind mount propagates an in-place append with a short delay. The
demo waits for the container to read the same bytes before it sends the
signal; the ten-second stat tick is longer than the delay, so it reads the
new bytes.

## Restart-bound edits

Editing a running listener (its address, upstream, protocol or TLS),
renaming one, or changing anything outside the listeners and the rules
(audit, admin, log_level, the top-level analyzer) logs `restart to apply it`
naming the listener, and the process keeps serving what it had. The sidecar
README's "Editing the config file of a running process" has the full list.
