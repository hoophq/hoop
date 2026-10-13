# x-sqlframe: SQL in a binary frame, in freestanding C++

A made-up protocol that wraps SQL text in a fixed binary header. The plug-in shows two things a C or C++ author needs and no SDK provides: the host SQL classifier through `hoop_split_sql` and `hoop_analyze_sql`, and a `filter` that removes keepalive padding before the decoder sees it. The module is one C++17 file over `../../sdk/c/hoop_codec.h`: no libc, no STL, no exceptions, no RTTI.

## Protocol

Every frame is a 6-byte header and a payload:

| offset | size | field |
|---|---|---|
| 0 | 1 | magic, `0x5A` |
| 1 | 1 | type |
| 2 | 4 | payload length, big-endian |
| 6 | n | payload |

Client frames:

| type | name | payload | statement |
|---|---|---|---|
| `0x01` | QUERY | SQL text, one or more statements separated by `;` | one per statement, classified by the host |
| `0x02` | PREPARE | same as QUERY | same, command `PREPARE` |
| `0x03` | PING | none | `PING`, operation `other` |
| `0x7F` | CLOSE | none | `CLOSE`, operation `other` |

Server frames:

| type | name | payload | statement |
|---|---|---|---|
| `0x81` | ROWS | u32 big-endian row count | `ROWS`, operation `other`, `result.row_count` |
| `0x82` | OK | none | `OK`, operation `other` |
| `0x8E` | ERROR | message | `ERROR: <message>`, operation `other` |

A wrong magic byte, a type the direction does not define, a ROWS payload that is not four bytes, or a length above `max_reassembly` (1 MiB) is a decode error and the Sidecar drops the connection. A frame cut short is not: `decode` stops before it and the Sidecar retries with more bytes.

Either side may send `0x00` bytes between frames as keepalive padding. The `filter` export strips them in both directions. It tracks a cursor per direction, so a zero byte inside a payload passes through under any chunking.

A denial is an ERROR frame carrying the rule's message.

### Statement fields

A QUERY or PREPARE payload goes to `hoop_split_sql`, then each part to `hoop_analyze_sql`. The statement copies `operation`, `effects`, `relations` and `tables` from the analysis, with the part as its `text`. Every statement carries:

| metadata key | value |
|---|---|
| `x-sqlframe.command` | the frame name: `QUERY`, `PREPARE`, `PING`, `CLOSE`, `ROWS`, `OK`, `ERROR` |
| `x-sqlframe.seq` | frames completed in that direction, as a decimal, starting at 1 |
| `x-sqlframe.dialect` | the `dialect_label` option; QUERY and PREPARE only |
| `x-sqlframe.reason` | the classifier's reason when it could not read the statement end to end; QUERY and PREPARE only |

### Options

| name | type | default | effect |
|---|---|---|---|
| `dialect_label` | string | `postgres` | copied to `x-sqlframe.dialect` |

The manifest's `sql_dialect` is `postgres`, which picks the host lexer. The option is a label for rules and audit; it does not change the lexer.

## Build

The build needs clang with the wasm32 target and `wasm-ld` from LLVM's lld, version 15 or later. On macOS:

```sh
brew install llvm lld
export CC=/opt/homebrew/opt/llvm/bin/clang++
```

Apple's clang has no wasm target, so `CC` must name the Homebrew one. If `wasm-ld` is not on `PATH`, set `WASM_LD_DIR` to the directory that holds it.

```sh
./build.sh                          # writes dist/sqlframe.wasm and copies it to sqlframe.wasm
```

`build.sh` includes `hoop_codec.h` from `../../sdk/c` inside the hoop repository and from this directory in the copy under `hoophq/plugins`, where the release workflow places the header beside the source. The flags are `--target=wasm32-unknown-unknown -std=c++17 -nostdlib -fno-builtin -fno-exceptions -fno-rtti -O2 -fuse-ld=lld -Wl,--no-entry -Wl,--strip-all`. `-fno-builtin` keeps the optimizer from turning a copy loop into a `memcpy` call; the source defines `memcpy` and `memset` for the aggregate copies the compiler emits on its own.

`sqlframe.wasm` is checked in beside `fixtures.json`, so the conformance run below needs no clang or lld. The plug-in workflow rebuilds the module from source on every change under this directory or the SDK and proves it against the fixtures; the checked-in copy is what `make test-sidecar-plugins` proves.

### How the source is laid out

- `HOOP_CODEC_ARENA` from the header provides `alloc` and `free`. The arena lives one export call at a time: `ArenaCall` at the top of every export calls `hoop_arena_reset()` on the way out, and the next call's first allocation rewinds it. A region a host import returned therefore stays readable until the export returns, however much the export allocates after taking it. Per-connection state (`conn_state`) is static and never lives in the arena.
- `Writer` builds the output JSON in the arena and grows by doubling. The block it abandons dies with the call like every other region; `free` is a no-op on a bump arena.
- `find_key`, `next_elem` and `unescape` are the whole JSON reader. The analysis the host returns is spliced into the statement as raw spans, so the fields reach the Sidecar byte for byte as the host wrote them.
- Every ABI export carries `HOOP_EXPORT`, so the link needs no `--export` flags and nothing else leaks into the export list.

## Test

```sh
hoop start sidecar --codec-test sqlframe.wasm fixtures.json
```

Inside the hoop repository `make test-sidecar-plugins` runs the same check for every SDK and example with a freshly built `hoop-inspect`.

`fixtures.json` covers a single query, a two-statement query split by the host, PREPARE, PING and CLOSE, the option in metadata, the three server frames, two frames in one read, a partial frame, padding before a frame in each direction, and four malformed inputs. All eight checks pass; check 7 replays each script one byte at a time, which is what proves the filter cursor and the partial-frame rule.

## Run

```sh
hoop start sidecar --config config.yaml --validate
hoop start sidecar --config config.yaml
```

```yaml
plugins:
  - protocol: x-sqlframe
    module: ./sqlframe.wasm

listeners:
  - name: sqlframe
    protocol: x-sqlframe
    listen: 127.0.0.1:19500
    upstream: sqlframe-db.internal:9500
    plugin:
      dialect_label: pg16
    guardrails:
      rules:
        - name: no-destructive-sql
          type: operation
          operations: [drop, truncate, delete]
          message: destructive statements are refused on sqlframe-db
```

## What each rule type can fence

| rule type | on this lane |
|---|---|
| `operation` | the classifier's verdict on QUERY and PREPARE text: `select`, `insert`, `drop`, ...; `other` for PING, CLOSE and every server frame; `unknown` when the lexer could not read the SQL |
| `table` | the relations the classifier found, lowercased, with read or write access |
| `metadata` | `x-sqlframe.command` to fence a frame type, `x-sqlframe.dialect` to fence a label |
| `deny_words_list`, `pattern_match`, `pii` | the statement text: the SQL, or `ERROR: <message>` on the server side |

Two rules together, when the Sidecar carries a license (the free tier enforces one):

```yaml
        - name: no-destructive-sql
          type: operation
          operations: [drop, truncate, delete]
          message: destructive statements are refused on sqlframe-db
        - name: no-prepare
          type: metadata
          metadata:
            x-sqlframe.command: [PREPARE]
          message: prepared statements are refused on sqlframe-db
```

`http_*` and `grpc_status` rules do not apply: the protocol has no request path, status or trailer. The module declares no `rewrite`, so a `mask` block on the lane fails validation.
