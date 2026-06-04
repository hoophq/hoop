# tunnel/dist/scripts

Install / uninstall scripts shipped inside the release tarball.

These are user-facing wrappers around `hsh-tunneld install` and
`hsh-tunneld uninstall`. They live here (rather than in `scripts/dev/`)
because they are *shipped artifacts*: RD-227 copies them into the
combined `hsh-{linux,...}.tar.gz` archive that the `hoophq/hsh` GitHub
Release publishes.

## Files

| File           | Role                                                    |
|----------------|---------------------------------------------------------|
| `install.sh`   | Locates the sibling `hsh-tunneld`, self-elevates with `sudo`, runs `hsh-tunneld install`, prints next-steps. POSIX `/bin/sh` (works on dash/busybox). |
| `uninstall.sh` | Same shape, runs `hsh-tunneld uninstall`. Looks at `/usr/local/bin/hsh-tunneld` first so it works after the tarball directory is gone. |

## Tarball layout (the contract RD-227 implements)

```
hsh-linux-x64/
  hsh                   # unprivileged CLI (Bun binary)
  hsh-tunneld           # privileged daemon (Go binary)
  install.sh            # this directory
  uninstall.sh
  LICENSE
  README.md             # user-facing docs (separate from this file)
```

The scripts use `$(dirname "$0")` to locate `hsh-tunneld`, so the layout
above is the minimum — additional files in the same directory are
ignored.

## Why not put the install logic in shell

Every step the scripts perform is delegated to `hsh-tunneld install`
(or `uninstall`). The Go binary already knows how to:

- write the systemd unit
- create the `hsh` group
- copy itself into `/usr/local/bin`
- enable + start the service

Replicating that in shell would mean two implementations to maintain
and two places where bugs hide. The scripts add only the things shell
is good at: locating siblings via `dirname "$0"`, re-execing under
sudo, and printing colourful next-steps.

## Local test recipe

The same Ubuntu+systemd container we use for RD-217 covers this
ticket too. Build a linux/amd64 daemon, drop it next to the script,
bind-mount into the container, run `./install.sh`:

```
go build -o /tmp/tarball/hsh-tunneld ./tunnel/cmd/hsh-tunneld
cp tunnel/dist/scripts/{install,uninstall}.sh /tmp/tarball/
chmod +x /tmp/tarball/{install,uninstall}.sh
docker run --rm --privileged --cgroupns=host \
  --tmpfs /run --tmpfs /run/lock \
  -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
  -v /tmp/tarball:/opt/tarball:ro \
  hsh-systemd-smoke:24.04 \
  bash -c "cd /opt/tarball && ./install.sh"
```

For an end-to-end run that exercises install + status + uninstall in
one shot, the runtime smoke at `tunnel/dist/scripts/test/run-smoke.sh`
script does the full cycle inside the container.
