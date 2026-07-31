# Custom agent images (`hoophq/hoopdev-minimal`)

Hoop publishes two agent images:

| Image | Contents | Use it when |
| --- | --- | --- |
| `hoophq/hoopdev` (default) | Agent binary **plus** a broad set of bundled database and cloud CLIs (psql, mysql, mongosh, kubectl, aws, gcloud/bq, ...). | You want everything preinstalled and accept the larger image / CVE surface those stacks carry. |
| `hoophq/hoopdev-minimal` | Agent binary plus a small runtime only (tini, ca-certificates, locales). **No** bundled database or cloud CLIs, no `ssh`/`envsubst`, no Node, no OCR, no build/-dev packages. | You want a lean, low-CVE production base and will add only the client binaries your connections actually need. |

`hoophq/hoopdev-minimal` is **opt-in and additive** — the default image and every
existing deployment are unchanged.

## What works out of the box

The minimal image serves any connection type that the agent handles inside its
own binary (via the embedded `libhoop` proxy), with no external client process:

- **Native database proxy** — `postgres`, `mysql`, `mssql`, `mongodb`, `oracle`.
  The agent speaks the wire protocol directly; it does **not** shell out to a CLI.
- **HTTP proxy** connections.
- **TCP proxy** connections.

## What needs extra binaries

Anything where the agent shells out to a third-party client is **not** included
and must be added in a derived image:

- **Exec / shell-based sessions** (`bash`, `python`, custom command connections)
  and their runtimes.
- **CLI database clients** the native proxy does not cover, e.g.
  `clickhouse-client`.
- **Cloud CLIs and their session helpers** — `kubectl` (kubernetes-exec),
  `aws` (ECS Exec / SSM), `gcloud`/`bq` (BigQuery), etc.

If you need a large set of these, use the default `hoophq/hoopdev` image instead
of re-adding them to the minimal one.

## Building a custom agent

Extend the minimal image and install only the clients you use. The base already
runs as the unprivileged `hoop` user (uid/gid 10001), so switch to `root` for
`apt-get` and switch back before the end.

```dockerfile
# Pin to a released tag (e.g. the version you deploy).
FROM hoophq/hoopdev-minimal:<tag>

# Add only the client(s) your connections need. Example: the PostgreSQL CLI
# for exec-based postgres sessions (the native DB proxy does NOT need this).
USER root
RUN apt-get update -y && \
    apt-get install -y --no-install-recommends postgresql-client && \
    rm -rf /var/lib/apt/lists/*
USER hoop

# ENTRYPOINT/CMD are inherited from the base:
#   ENTRYPOINT ["tini", "--"]
#   CMD ["hoop", "start", "agent"]
```

Build and push it to your own registry, then point the agent Helm chart at it:

```yaml
# values.yaml for the agent chart
image:
  repository: myorg/hoop-agent-custom
  tag: <your-tag>
```

To run the stock minimal image (no extra clients) via the chart, use the opt-in
toggle instead of overriding the repository:

```yaml
image:
  minimal: true
```

An explicit `image.repository` always takes precedence over `image.minimal`.

## Image tags and patch cadence

`hoophq/hoopdev-minimal` is republished on every release and **re-patched weekly**
between releases: a scheduled job rebuilds it against current OS security updates
(`apt-get upgrade`) and, only if the OS layer is free of fixable CRITICAL/HIGH
vulnerabilities, republishes these tags:

| Tag | Moves? | Use it when |
| --- | --- | --- |
| `:latest` | Yes — latest weekly OS patches | You want the most-patched base and rebuild your derived image regularly. |
| `:<release>-<YYYYMMDD>` | Once per weekly rebuild | You want a base pinned to a specific week's patch set. For a guarantee the bytes never change, pin by digest (`@sha256:...`) — a manual same-day rebuild republishes the dated tag. |
| `:<release>` | No — the original release image | You need the exact bytes shipped at that release (no post-release OS patches). |

For fully reproducible builds, pin by digest (`hoophq/hoopdev-minimal@sha256:...`);
a digest always refers to the same bytes regardless of which tags point at it.

The shipped agent binary's own dependency/toolchain CVEs are fixed by cutting a
release (dependency bumps gated by `govulncheck`), not by the weekly OS rebuild —
so track releases for those and `:latest` for OS patches.
