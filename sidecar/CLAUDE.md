# CLAUDE.md: sidecar

Wire-protocol inspection as a library. It turns raw database bytes into
structured statements, and statements into allow/deny verdicts, masked
responses and an audit trail.

The root `CLAUDE.md` does not cover this directory. It describes the product
modules under `go.work`; `sidecar/` contributes seven more entries and follows
different rules. This file governs everything under `sidecar/`.

The directory was called `hoopinspect/` until the CLI command became
`hoop start sidecar`. Nothing about the module changed with the name, but two
packages moved so the tree stops stuttering: the inspection core is
`sidecar/inspect` (it was the root package) and the assembly that was
`hoopinspect/sidecar` is `sidecar/daemon`. The binary, its config directory
and `HOOP_INSPECT_CONFIG` all keep the `hoop-inspect` spelling, because
renaming those breaks running deployments.

Full documentation is `README.md` here (68 KB, read the section you need, not
the whole thing).

## The invariant: exactly one dependency

`github.com/hoophq/hoop/sidecar` depends on `github.com/hoophq/libhoop`
and nothing else. It used to depend on nothing at all, and that was the
product pitch — vendorable without a supply-chain review, readable end to end
in an afternoon, compilable to `wasip1`. The protocol codecs moving to libhoop
ended it: this module cannot describe its own inputs without naming the types
the decoders produce.

libhoop is PRIVATE, so building or testing here needs
`GOPRIVATE=github.com/hoophq/libhoop` and credentials for that repository.

**The edge runs one way and must stay that way.** libhoop imports nothing from
this repository. Two mechanisms hold that:

- **Types are aliased, not copied.** `inspect/wiretypes.go` declares
  `type Statement = codectypes.Statement` and so on. Same type, not a
  conversion — which is why a libhoop codec satisfies the `Codec` interface
  here structurally, without importing this package, and why there is one
  definition of the document a policy evaluates rather than two that drift.
- **Behaviour is injected, not imported.** The SQL classifier (`AnalyzeSQL`)
  and the lexer stay here, because they are the most safety-critical code in
  the system and there must be one auditable copy. `sidecar/codec/*` hands
  them to each decoder through its `Options`.

Check it:

```bash
cd ../libhoop && grep -rn '"github.com/hoophq/hoop' --include='*.go' .   # must be empty
```

**Each optional component is its own module, so it can be plugged in or
dropped without touching anything else.** SQLite is the clean case: removing
it is removing a directory.

| Module | Carries |
|---|---|
| `cmd/` | the `hoop-inspect` binary, where the optional plugins are linked |
| `config/yaml/` | `gopkg.in/yaml.v3` |
| `pii/alcatraz/` | `github.com/hoophq/alcatraz` |
| `store/sqlite/` | `modernc.org/sqlite`, pure Go because the sidecar is a static binary |
| `analyzer/vertex/` | `golang.org/x/oauth2`, the only analyzer provider needing one |
| `lexer/conformance/` | PostgreSQL's real parser, test-only |

Adding a dependency to the root still needs a reason. Add a nested module, or
do without.

## Commands

`make test-sidecar` at the repo root runs every module below, and
`make test-oss` depends on it, so CI runs them too. The commands are here
because the target is a loop over them and you will want one at a time.

```bash
# root module (inspect/, lexer/, codec/, policy/, gate/, proxy/, daemon/, ...)
go test ./...
go vet ./...

# nested modules are NOT reached by the line above
for m in cmd config/yaml pii/alcatraz store/sqlite analyzer/vertex lexer/conformance; do
  (cd "$m" && CGO_ENABLED=0 go test ./...)
done

# the oracle: run this for ANY change under lexer/
(cd lexer/conformance && CGO_ENABLED=0 go test ./...)

# validate a sidecar config without starting anything
(cd cmd && go run . -validate -config /path/to/config.yaml)
```

## Gotchas

- **`go test ./...` at the root reaches no nested module.** Separate `go.mod`
  files are what keeps optional dependencies out of the root, and the cost is that
  every nested module needs its own invocation. See the loop above.

- **CI reaches this only through `test-sidecar`.** `make test-oss` runs
  `go test github.com/hoophq/hoop/...`, which does not match module
  `github.com/hoophq/hoop/sidecar`, so the Makefile carries a second target
  that walks every `go.mod` under `sidecar/` and `test-oss` depends on
  it. Break that dependency and these tests stop running everywhere except
  on your machine.

- **A `lexer/` change is not verified until the conformance suite passes.**
  `lexer/conformance/` runs PostgreSQL's own parser and the scanner over the
  same statements and fails on disagreement. The scanner is hand-written and
  deliberately not a parser, so the oracle is the only thing standing between
  a plausible change and a wrong one. It runs under `CGO_ENABLED=0` because
  the parser is a wasm build.

- **A false positive in the lexer is an outage, not a warning.** A statement
  misread as a write is refused by a read-only lane. Reserved words are legal
  column aliases when `AS` is written, and BI tools emit them: Metabase's
  schema sync sends `has_table_privilege(..., 'delete') AS delete`. Widening
  the classifier costs an operator their tool. Add the case to
  `lexer/conformance` corpus in `oracle_test.go` and let the real parser judge.

- **Codecs register a factory, not an instance** (`inspect/registry.go`). Codecs that
  reassemble messages across packets hold per-connection state, so a shared
  instance would let one connection's SQL surface in another's audit trail.
  `Register` panics on a duplicate protocol, because import order deciding a
  winner is worse than a build failure.

- **`inspect` imports no codec, and no longer ships one.** The decoders
  live in `github.com/hoophq/libhoop`, a private module, under its
  `v2/codec/*` directory — `v2` there is a folder name, not a major version.
  libhoop imports NOTHING from this repository; the types it returns are
  defined there and aliased here, so its codecs satisfy `Codec` structurally.

- **`sidecar/codec/*` is the registration seam, not a decoder.** libhoop
  cannot call `Register`, so these thin packages do it, and they are also
  where `AnalyzeSQL` and the lexer get injected into a decoder. Import
  `codec/all` for every protocol, or one package for one protocol so a binary
  fronting Postgres never links the TDS and HTTP machinery.

- **Construct codecs through the seam, never libhoop directly.** A decoder
  built with the zero `Options` has no classifier: it reports statement text
  with `OpUnknown`. That fails closed, but it means a policy naming `select`
  matches nothing.

- **The license verifier exists twice, on purpose.** `sidecar/license`
  reimplements `common/license` over the stdlib: same key, same RSA-PSS
  signature over the same bytes, same JSON. Importing the gateway's module
  would end the one-dependency invariant, so the copy is the price. Drift
  locks paying customers out of features they bought and neither module can
  see it, so `client/licensecompat` pins the two. Touch the key or the
  signing data on one side and you MUST touch the other.

## Layout

The module root holds no Go files: `go.mod`, this file and `README.md` only.

| Path | Does |
|---|---|
| `inspect/` | bytes to statements, plus the codec registry; the wire vocabulary every other package names |
| `lexer/` | SQL text to an effect and a relation list, without a grammar |
| `codec/` | registration seam: wires libhoop's decoders to the classifier |
| `policy/` | statement to verdict; local rules, then OPA |
| `license/` | verifies the signed license that lifts the rule caps; a stdlib twin of `common/license` |
| `analyzer/` | the model-backed evaluator, third in the policy chain |
| `gate/` | orders inspection, policy, audit and masking into one decision |
| `proxy/` | TCP relay that pumps both directions through a Gate |
| `daemon/` | assembles the relay from config — sinks, evaluators, listeners — and is the CLI entry point |
| `session/` | one inspected connection and the identity behind it |
| `audit/` | the write side of the trail |
| `store/` | the read side |
| `pii/` | detectors and maskers |

## Conventions

- Comments say **why**, at length where the reasoning is not recoverable from
  the code. This module's comments carry design rationale on purpose; match
  that register rather than stripping it.
- Tests use `testing` only. No assertion library, in any module that the root
  can reach.
- A masking or policy change needs a test that would fail without it. `gate/`
  and `inspect/bypass_test.go` are where behaviour that an operator sees is
  pinned.
- `inspect/bypass_test.go` holds statements that evade classification. A new
  evasion belongs there, phrased as the verdict an operator would get.

## Running it

Two compose stacks exercise the relay end to end, both outside this directory:

- `deploy/docker-compose/envoy-stack/` : Envoy plus OPA plus the sidecar. It
  carries more than the Envoy demo, and the parts that do not need Envoy
  cannot be run on their own today. Splitting it is open work.
- `deploy/docker-compose/metabase-stack/` : Metabase against a masked
  warehouse, with an asserting `./demo.sh`.

They share one image and one Dockerfile, so building `hoop-inspect:local` in
either serves the other.

`scripts/dev/` holds four ordered scripts for the Vertex analyzer, which spend
real GCP calls. Read `scripts/dev/README.md` before running them.
