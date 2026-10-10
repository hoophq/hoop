# hoop codec plug-in ABI, version 1

A codec plug-in is one WebAssembly module. The relay (`codec/wasm`, the
host) instantiates it, hands it the bytes of one connection, and reads back
the statements policy evaluates. The module never opens a socket, never
sees another connection's bytes, and never reaches the host except through
the imports below.

The ABI is a C ABI over linear memory with JSON payloads. Every language
with a wasm32 target implements it: the SDKs under `../sdk/` are
conveniences.

## Memory and calling convention

- The guest owns its linear memory and exports it as `memory`.
- The host places input by calling `alloc(len) -> ptr`, writing, then
  passing `(ptr, len)`. After the call returns the host calls
  `free(ptr, len)`.
- The guest returns output as one `u64`: `(ptr << 32) | len`. The host reads
  `len` bytes at `ptr` and then calls `free(ptr, len)`. `0` means no output.
- A host import that returns bytes does the same in reverse: the host calls
  the guest's `alloc`, writes, and returns the packed `u64`; the guest frees
  it.
- Every string is UTF-8. Every JSON payload is an object.
- Calls are serialized: the host never calls two exports of one instance
  concurrently, and a host import runs on the stack of the export that
  called it.

## Exports

Required:

| export | signature | purpose |
|---|---|---|
| `memory` | memory | linear memory |
| `alloc` | `(len: u32) -> u32` | allocate `len` bytes the host will write |
| `free` | `(ptr: u32, len: u32)` | release a region allocated by `alloc` or returned to the host |
| `describe` | `() -> u64` | the Manifest JSON below. Called once, before any other export, on a throwaway instance |
| `decode` | `(conn: u32, dir: u32, ptr: u32, len: u32) -> u64` | decode bytes; returns DecodeResult JSON |

Optional, each announced by a name in the manifest's `capabilities`. A
capability named without its export, or an export present without its
capability, fails load:

| capability | export | signature | purpose |
|---|---|---|---|
| `deny` | `deny` | `(conn, dir, ptr, len) -> u64` | render the UTF-8 message at `(ptr,len)` as the protocol's native error frame, bytes out |
| `filter` | `filter` | `(conn, dir, ptr, len) -> u64` | transform bytes before inspection and forwarding; bytes out. May hold a prefix and return nothing |
| `rewrite` | `enable_rewrite`, `rewrite`, `flush` | `enable_rewrite(conn)`, `rewrite(conn, ptr, len) -> u64`, `flush(conn) -> u64` | response masking; see Masking |
| `credential` | `take_credential` | `(conn, ptr, len) -> u64` | lift the credential of the request statement at `(ptr,len)` (Statement JSON); returns CredentialResult JSON |
| `content` | `content` | `(conn, ptr, len) -> u64` | render a Statement JSON for the AI analyzer; returns ContentResult JSON |

Optional, not a capability:

| export | signature | purpose |
|---|---|---|
| `open` | `(conn: u32, ptr: u32, len: u32) -> u32` | a connection begins; `(ptr,len)` is the Options JSON object (string → string). Non-zero refuses the connection |
| `close` | `(conn: u32)` | a connection ended |
| `_initialize` | `()` | run once per instance before anything else, if present (wasi reactor convention) |

`dir` is `0` for bytes from the client and `1` for bytes from the server.

`conn` identifies a connection within one instance. Under
`instances: per_connection` (the default) every instance serves one
connection and `conn` is always `0`. Under `instances: per_lane` one
instance serves every connection of the lane and the guest keys its state
by `conn`; `open` and `close` bracket each one.

## Imports, module `hoop`

| import | signature | purpose |
|---|---|---|
| `analyze_sql` | `(ptr, len) -> u64` | classify SQL text with the host lexer in the manifest's `sql_dialect`; returns SQLAnalysis JSON |
| `split_sql` | `(ptr, len) -> u64` | split a multi-statement text with the host lexer; returns a JSON array of strings |
| `mask` | `(cptr, clen, vptr, vlen) -> u64` | mask one cell: column name at `(cptr,clen)`, value at `(vptr,vlen)`; returns the value to forward. Only inside `rewrite`/`flush`; a trap elsewhere |
| `log` | `(level: u32, ptr, len)` | a log line at level `0` debug, `1` info, `2` warn, `3` error, tagged with the lane and connection |

A module that imports anything else fails load, except `wasi_snapshot_preview1`
when the manifest says `"wasi": true`. WASI then sees no filesystem, no
network, no environment and no arguments; `clock_*`, `random_get`,
`fd_write` to 1 and 2 (forwarded to the log), `proc_exit` and the memory
functions work.

## Manifest (`describe`)

```json
{
  "abi": 1,
  "protocol": "x-acmewire",
  "label": "Acme Wire",
  "version": "1.3.0",
  "capabilities": ["deny", "rewrite"],
  "sql_dialect": "mysql",
  "max_reassembly": 8388608,
  "instances": "per_connection",
  "wasi": false,
  "call_timeout_ms": 2000,
  "memory_limit_pages": 1024,
  "options": [
    {"name": "compat_mode", "label": "Compat mode", "type": "enum",
     "values": ["v3", "v4"], "default": "v4", "help": "..."}
  ]
}
```

| key | required | meaning |
|---|---|---|
| `abi` | yes | must be `1` |
| `protocol` | yes | the protocol name; MUST start with `x-`, then `[a-z0-9_-]+`. The host refuses a built-in name at load, and the control plane grants `x-` protocols by the `plugins` capability alone |
| `label` | yes | human name for the listener form |
| `version` | no | the plug-in's own version, for the startup log |
| `capabilities` | no | the optional exports present; see above |
| `sql_dialect` | no | `postgres`, `mysql`, `mssql`, `clickhouse`, `oracle`, `googlesql`; required when the module imports `analyze_sql` or `split_sql` |
| `max_reassembly` | no | the largest partial message the host buffers before failing the stream; default 8 MiB |
| `instances` | no | `per_connection` (default) or `per_lane` |
| `wasi` | no | the module imports `wasi_snapshot_preview1` |
| `call_timeout_ms` | no | deadline per export call; default 2000 |
| `memory_limit_pages` | no | 64 KiB pages the instance may grow to; default 1024 (64 MiB) |
| `options` | no | per-listener settings the form renders and `open` receives; `type` is `string`, `int`, `bool` or `enum` |

## Payloads

Field names are the JSON names of `inspect.Statement`, which are libhoop's.
The host refuses unknown fields.

DecodeResult, from `decode`:

```json
{
  "statements": [
    {
      "direction": "client",
      "text": "PURGE orders WHERE age > 90d",
      "operation": "delete",
      "effects": ["delete"],
      "relations": [{"name": "orders", "access": "write"}],
      "tables": ["orders"],
      "database": "shop",
      "result": {"columns": [{"name": "id"}], "row_count": 3, "truncated": false},
      "metadata": {"x-acmewire.verb": "PURGE", "x-acmewire.seq": "17"}
    }
  ],
  "consumed": 42,
  "error": ""
}
```

- The host fills `protocol` and refuses it when present: a module cannot
  impersonate another protocol.
- `direction` defaults to the call's `dir`; `client` or `server`.
- `operation` MUST be one of the values `inspect.Operations()` lists
  (`select`, `insert`, `update`, `delete`, `create`, `drop`, `alter`,
  `truncate`, `grant`, `revoke`, `call`, `show`, `set`, `begin`, `commit`,
  `rollback`, `get`, `post`, `put`, `patch`, `head`, `options`, `connect`,
  `trace`, `exec_line`, `env_set`, `sftp_*`, `ws_message`, `ws_close`,
  `other`, `unknown`). Anything else is a decode error. Put the native verb
  in `metadata["<protocol>.verb"]`; a `metadata` rule matches it.
- The guest lowercases table and relation names; the policy compares
  as-is on the statement side.
- `consumed` counts bytes of the input the guest finished with. An
  incomplete trailing message is NOT an error: return the statements before
  it and stop `consumed` at its first byte. The host retains the rest and
  passes it again, prefixed to the next read, until `max_reassembly`.
- `error` non-empty means the bytes are malformed for this protocol. The
  host drops the connection and ignores `consumed` and `statements`.

SQLAnalysis, from `analyze_sql`:

```json
{"operation": "select", "effects": ["select"],
 "relations": [{"name": "orders", "access": "read"}], "tables": ["orders"],
 "complete": true, "reason": ""}
```

`complete: false` means the scan could not finish and `operation` is
`unknown`; `reason` says why. Copy these fields onto the statement.

CredentialResult, from `take_credential`:

```json
{"credential": "Bearer eyJ...", "ok": true, "statement": { ...the statement with the credential removed... }}
```

`ok: false` means the request carried none. When `ok` is true `statement`
MUST be present and MUST carry no trace of the credential; the host
replaces the statement with it before policy, audit or the analyzer see it.

ContentResult, from `content`:

```json
{"text": "Protocol: x-acmewire\nOperation: delete\n\nPURGE orders ...", "cache_key": "d41d8cd9", "ok": true}
```

`ok: false` means nothing to classify. `cache_key` names the statement's
SHAPE with literals stripped, so one verdict serves repeats; empty means
never cache.

RewriteResult, from `rewrite` and `flush`:

```json
{"bytes": "<base64 of the bytes to forward>", "cells": 12, "rows": 3}
```

`bytes` MAY be empty: the guest holds rows until it can rebuild them.
`flush` releases everything held; the host calls it when the connection ends.
`cells` counts the values `mask` returned CHANGED (compare the bytes it
returned with the bytes it was given); `rows` counts the rows holding at
least one of them. They feed the audit trail's masked counts, so a cell
the masker left alone is not one. An `error` field, non-empty, fails the
stream closed.

DenyFrame, from `deny`: raw bytes, no JSON.

Filter, from `filter`: raw bytes, no JSON. A trap fails the stream closed.

## Masking

The host enables the `rewrite` path with `enable_rewrite(conn)` before any
server bytes arrive, and only when the lane has a masker. The guest then
receives every server chunk through `rewrite` AFTER `decode` saw it, calls
`mask(column, value)` for each cell it can name, and returns rebuilt
frames. Column is empty when the protocol does not name one. The returned
value may differ in length: the guest recomputes every length prefix.
The host forwards values the guest does not pass through `mask` unmasked;
the guest decides which bytes are data.

## Failure semantics

All of these fail closed, and none of them reaches another connection or
lane:

- A trap (unreachable, out-of-bounds, stack overflow), a call past
  `call_timeout_ms`, or memory above `memory_limit_pages` closes the
  instance. The call reports an error; the host drops the connection with
  the deny frame if `deny` is available and a close otherwise. Under
  `per_lane` the lane refuses new connections until the host rebuilds the
  instance.
- Malformed output (not JSON, unknown field, an `operation` outside the
  list, `protocol` present) is a decode error for that connection.
- `describe` failing, a capability without its export, an export without
  its capability, an undeclared import, a `protocol` without the `x-`
  prefix, or a sha256 mismatch refuses the module at load, so the lane
  never starts.

## Conformance

`hoop-inspect -codec-test <module.wasm> [fixtures.json...]` proves the
contract above against a module. The checks that need no knowledge of the
protocol run on every module; the ones that need bytes run over the
author's fixtures.

Fixture file, one or more scripts:

```json
[
  {
    "name": "purge is a delete",
    "options": {"compat_mode": "v4"},
    "steps": [
      {"dir": "client", "hex": "50000000064f52444552...",
       "expect": {"consumed": 11, "statements": [{"operation": "delete", "tables": ["orders"],
                  "metadata": {"x-acmewire.verb": "PURGE"}}]}},
      {"dir": "server", "hex": "52...", "expect": {"consumed": 9, "statements": [{"result": {"row_count": 3}}]}},
      {"dir": "client", "hex": "ff00", "expect": {"error": true}}
    ]
  }
]
```

`expect.statements` compares only the fields it names. `expect.error` true
means `decode` must report an error.

Generic checks, every module:

1. `describe` returns a valid manifest; exports and imports match it.
2. `decode` of zero bytes consumes nothing, returns nothing, and does not
   fail.
3. `deny`, when declared, returns at least one byte for a message.
4. Every `operation` a fixture produces is in the allowed list.
5. A trap in the guest surfaces as an error; the host neither hangs nor panics.

Fixture-driven checks:

6. Each script, replayed whole, matches its expectations.
7. Each script, replayed one byte at a time with the host's reassembly,
   produces the same statements: the partial-input rule.
8. Two scripts interleaved step by step on two connections produce, per
   connection, what each produces alone: no state leaks between
   connections, under either instancing mode.
