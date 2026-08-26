# lexer/conformance

PostgreSQL's own grammar, checking the hand-written scanner in `../`.

## Rationale

`lexer` is a one-pass scanner over a labelled region stack, not a parser, and
it never will be one. That shape is right for the question a policy asks and
wrong for SQL in general, so the trade is defensible exactly as long as
somebody keeps checking it against a real grammar. This module parses every
fixture with PostgreSQL 17 and compares.

It is a separate module because the root ships with zero dependencies, test
dependencies included. Nothing here is importable from anything the root
builds. `cd hoopinspect && go build ./... && cat go.mod` still shows no
requires and no `go.sum`.

`wasilibs/go-pgquery` rather than `pganalyze/pg_query_go` directly: the
pganalyze module is cgo, and a conformance suite that only runs where a C
toolchain is configured is a conformance suite that stops running. wasilibs
compiles the same libpg_query to WebAssembly and runs it under wazero, so
`CGO_ENABLED=0` works and the suite runs in the same CI job as the wasip1
build. (The pganalyze module is still a dependency, for the generated
protobuf node types. Its cgo entry points sit behind `//go:build cgo` and are
never reached here.)

## Running it

```sh
cd hoopinspect/lexer/conformance
CGO_ENABLED=0 go test ./... -count=1
```

Fuzz the robustness properties:

```sh
CGO_ENABLED=0 go test -run '^$' -fuzz FuzzAnalyze -fuzztime 60s
```

Run against PostgreSQL's regression suite. Those 4.4 MB of another project's
fixtures are not vendored, so point the test at a copy:

```sh
curl -sSL https://codeload.github.com/postgres/postgres/tar.gz/refs/tags/REL_17_5 \
  | tar -xz -C /tmp --strip-components=5 postgres-REL_17_5/src/test/regress/sql
CGO_ENABLED=0 PG_REGRESS_SQL=/tmp/sql go test ./... -count=1 -run Regression -v
```

The test also reads `corpus/regress/*.sql` if you would rather keep a copy
in-tree. Without either it skips; it never invents a corpus.

Baseline at REL_17_5, so you can tell drift from noise:

```
144/224 files usable (80 rejected by pg itself), 20391 statements,
20240 complete (99.3%)
  incomplete:    94  prepared statement; the text was supplied elsewhere
  incomplete:    36  stored procedure; body is in the catalog
  incomplete:    18  anonymous code block; body is interpreted at runtime
  incomplete:     3  unbalanced parentheses at statement end
```

The 80 rejected files are psql scripts and deliberate syntax errors that
PostgreSQL itself will not parse, so there is no ground truth to split them
on. The three unbalanced-parenthesis concessions are all
`CREATE RULE ... DO INSTEAD (stmt; stmt)`, where a semicolon inside
parentheses closes a region the scanner is still standing in. It concedes,
which is the correct outcome for a construct it cannot follow.

## The asymmetric assertion

For every fixture, `oracle_test.go` requires:

> **EITHER** the scanner's write-set equals PostgreSQL's write-set,
> **OR** the scanner reported `Complete=false`.

Read the second clause carefully: it is the soundness contract rather than an
escape hatch. `Complete=false` fails the caller closed, so a scanner that
admits defeat has behaved correctly and the suite counts it as a concession
rather than a match. The suite refuses one thing: a confident, different
answer. `Complete=true` plus a write-set PostgreSQL disagrees with means a
policy guarding a relation did not see a write to it.

The suite asserts the scanner is never wrong, not that it is as precise as
PostgreSQL. It cannot be that precise, and a suite demanding it would be a
suite demanding a second PostgreSQL.

Only write-sets are compared. Read-sets legitimately differ: PostgreSQL's raw
parse tree sees `FROM cte_name` as a `RangeVar`, because CTE resolution
happens after parsing, while the scanner knows the name was bound by a `WITH`.
Both are right about what they can see, and neither difference can let a write
through.

The summary line reports exact / conceded / wrong with an agreement rate, per
group, and it is worth watching. A drop in the rate without a corresponding
drop in `wrong` is the scanner learning to give up; a rise in `wrong` is the
scanner learning to guess.

## Walking the oracle tree

`pg_query.Summary()` would hand over `(relation, read|write)` directly, but
wasilibs does not re-export it and the pganalyze original is behind
`//go:build cgo`. So `oracle.walk` does it.

Only the nodes that decide a WRITE get a case: `InsertStmt`, `UpdateStmt`,
`DeleteStmt` and `MergeStmt` write `.relation`, `CreateTableAsStmt` and
`SelectStmt` write `.into`, `TruncateStmt`/`DropStmt`/`GrantStmt` write their
object lists, and so on. Everything else descends generically over protobuf
message fields, and a relation found on the way down is a read. Generic
descent is what makes a data-modifying CTE visible without a special case:
`CommonTableExpr.ctequery` holds a DML node and the walk lands on it.

Descent skips consumed fields by NAME rather than enumerating the fields to
visit, so a field added by a future PostgreSQL is still walked, as a read,
instead of silently dropped.

Two judgment calls are encoded in the walker, both matching the scanner on
purpose:

- `EXPLAIN` without `ANALYZE` builds a plan and stops. Every write inside it
  demotes to a read. `EXPLAIN ANALYZE` executes, so it does not.
- `GRANT`/`REVOKE ... ON t` writes `t`. It rewrites the ACL, and "who may read
  customers" is the kind of change a policy guarding customers cares about.

## Robustness properties

`corpus_test.go` needs no oracle. Two properties hold for every input:

1. `Analyze` never panics. It sits on a data path in front of a database and
   the inputs are attacker-shaped.
2. A statement whose first word is a DML verb never returns `Complete=true`
   with no `Effects`. That pair reads as "I understood the whole statement and
   it does nothing", which is a lie the caller cannot detect. Not
   understanding is fine; it spells `Complete=false`.

Both are asserted over a hostile fixture table and exposed as `FuzzAnalyze`.

The regression-corpus test asserts neither relations nor verbs. Those files
exercise grammar corners no production client emits, and demanding agreement
there would be demanding the scanner BE PostgreSQL. It reports a Complete rate
so you can watch the number.
