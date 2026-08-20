# CLAUDE.md: hoopinspect

Wire-protocol inspection as a library. It turns raw database bytes into
structured statements, and statements into allow/deny verdicts, masked
responses and an audit trail.

The root `CLAUDE.md` does not cover this directory. It describes six modules
under `go.work`; `hoopinspect` contributes seven more and follows different
rules. This file governs everything under `hoopinspect/`.

Full documentation is `README.md` here (68 KB, read the section you need, not
the whole thing).

## The invariant: zero dependencies

`github.com/hoophq/hoopinspect` depends on the standard library and nothing
else. Test dependencies included. The library exists to be vendored without a
supply-chain review and read end to end in an afternoon. A dependency is a
breaking change to the product pitch, not a convenience.

Three checks, all cheap. The third is a canary rather than a build target: a
stdlib-only module compiles for a platform with no operating system, and one
that has picked up a dependency usually will not.

```bash
GOWORK=off go list -m all        # must print exactly one line: the module itself
ls go.sum                        # must not exist
GOOS=wasip1 GOARCH=wasm go build ./...
```

**Each optional component is its own module, so it can be plugged in or
dropped without touching anything else.** SQLite is the clean case: removing
it is removing a directory. The root staying at zero dependencies falls out of
that rather than causing it.

| Module | Carries |
|---|---|
| `cmd/` | the `hoop-inspect` binary, where the optional plugins are linked |
| `config/yaml/` | `gopkg.in/yaml.v3` |
| `pii/alcatraz/` | `github.com/hoophq/alcatraz` |
| `store/sqlite/` | `modernc.org/sqlite`, pure Go because the sidecar is a static binary |
| `analyzer/vertex/` | `golang.org/x/oauth2`, the only analyzer provider needing one |
| `lexer/conformance/` | PostgreSQL's real parser, test-only |

Adding a dependency to the root is never the answer. Add a nested module, or
do without.

## Commands

`make test-hoopinspect` at the repo root runs every module below, and
`make test-oss` depends on it, so CI runs them too. The commands are here
because the target is a loop over them and you will want one at a time.

```bash
# root module
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
  files are what keeps the root at zero dependencies, and the cost is that
  every nested module needs its own invocation. See the loop above.

- **CI reaches this only through `test-hoopinspect`.** `make test-oss` runs
  `go test github.com/hoophq/hoop/...`, which does not match module
  `github.com/hoophq/hoopinspect`, so the Makefile carries a second target
  that walks every `go.mod` under `hoopinspect/` and `test-oss` depends on
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

- **Codecs register a factory, not an instance** (`registry.go`). Codecs that
  reassemble messages across packets hold per-connection state, so a shared
  instance would let one connection's SQL surface in another's audit trail.
  `Register` panics on a duplicate protocol, because import order deciding a
  winner is worse than a build failure.

- **The root package imports no codec.** `codec/postgres` imports the root for
  its types, never the reverse. A binary links only the protocols it imports,
  usually through `codec/all` or one specific package.

## Layout

| Path | Does |
|---|---|
| `inspect.go`, `registry.go`, `sqlmeta.go` | the root package: bytes to statements, codec registry |
| `lexer/` | SQL text to an effect and a relation list, without a grammar |
| `codec/{postgres,mssql,http}` | wire decode and response rewrite, one package per protocol |
| `policy/` | statement to verdict; local rules, then OPA |
| `analyzer/` | the model-backed evaluator, third in the policy chain |
| `gate/` | orders inspection, policy, audit and masking into one decision |
| `proxy/` | TCP relay that pumps both directions through a Gate |
| `sidecar/` | assembles the relay from config: sinks, evaluators, listeners |
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
  and `bypass_test.go` are where behaviour that an operator sees is pinned.
- `bypass_test.go` holds statements that evade classification. A new evasion
  belongs there, phrased as the verdict an operator would get.

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
