# x-resp: Redis RESP2 in Rust

A hoop Sidecar codec plug-in for the Redis wire protocol, RESP2. It turns
each client command into one statement the lane's rules evaluate, each
server reply into one audit record, renders a RESP error when a rule
denies, masks bulk string values in replies, and lifts the AUTH password.

| manifest | value |
|---|---|
| protocol | `x-resp` |
| label | Redis RESP |
| capabilities | `deny`, `rewrite`, `credential` |
| instances | `per_connection` |
| option `max_text_bytes` | int, default `4096`: bytes of a command line kept as the statement text |
| option `max_inline_bytes` | int, default `65536`: longest inline command line before the LF, CR included; a longer one, complete or not, drops the connection as malformed (Redis's own bound) |

## Protocol

A client sends one command per RESP array of bulk strings:

```
*3\r\n$3\r\nSET\r\n$6\r\nuser:1\r\n$3\r\nbob\r\n
```

or per inline line (`PING\r\n`), which `redis-cli` and telnet use. The
server answers with a simple string (`+OK`), an error (`-ERR ...`), an
integer (`:3`), a bulk string (`$5\r\nhello\r\n`) or an array of those,
nested to any depth (`COMMAND`, `HGETALL`, `EXEC` all return arrays).

The lane speaks RESP2 only. A `HELLO 3` switches the server to RESP3,
whose `%`, `_`, `#` and `,` types this codec does not read; the next
reply is a decode error and the connection closes. A `metadata` rule on
`HELLO` keeps clients on RESP2.

## Statements

Client, one per command:

| field | value |
|---|---|
| `text` | the command line, arguments joined by spaces, cut at `max_text_bytes`. AUTH and the AUTH clause of HELLO omit the credential |
| `operation` | see the table below |
| `tables`, `relations` | the key arguments, lowercased: `read` for `select`, `write` for the rest |
| `metadata["x-resp.command"]` | the command, uppercased |
| `metadata["x-resp.key"]` | the first key, when the command takes one |
| `metadata["x-resp.credential"]` | AUTH: a per-connection handle, `"1"`, `"2"`, ... The password stays in the codec; `take_credential` resolves the handle |

| commands | operation | keys |
|---|---|---|
| GET TTL STRLEN HGET HGETALL LRANGE SMEMBERS ZRANGE TYPE | `select` | first argument |
| MGET EXISTS | `select` | every argument |
| SCAN | `select` | none |
| SET INCR DECR APPEND HSET LPUSH RPUSH SADD ZADD EXPIRE PERSIST | `update` | first argument |
| MSET | `update` | every other argument |
| DEL UNLINK | `delete` | every argument |
| HDEL LPOP RPOP SREM ZREM | `delete` | first argument |
| FLUSHALL FLUSHDB | `truncate` | none |
| KEYS INFO DBSIZE CLIENT COMMAND | `show` | none |
| CONFIG SELECT AUTH | `set` | none |
| MULTI / EXEC / DISCARD | `begin` / `commit` / `rollback` | none |
| EVAL EVALSHA | `call` | the `numkeys` arguments after the script |
| PING ECHO QUIT and anything else | `other` | none |

Server, one per top-level reply, operation `other`:

| reply | `text` | `metadata["x-resp.reply"]` |
|---|---|---|
| `+OK\r\n` | `+OK` | `simple` |
| `-ERR unknown command\r\n` | `-ERR unknown command` | `error` |
| `:3\r\n` | `:3` | `integer` |
| `$5\r\nhello\r\n` | `$5` (`$-1` for null) | `bulk` |
| `*2\r\n...` | `*2` (`*-1` for null), `result.row_count` 2 | `array` |

Values never reach the audit trail: a bulk string reports its length.

## Capabilities

- `deny`: `-ERR <message>\r\n`, with line breaks in the message turned
  into spaces so the frame ends where the client expects.
- `rewrite`: every bulk string payload in a reply, at any depth, goes
  through the lane's masker with column name `""`, and the codec rewrites
  the `$len` prefix. `cells` counts payloads the masker changed; `rows`
  counts a changed top-level bulk string as one row and each changed
  element of a top-level array as one.
- `credential`: AUTH and the AUTH clause of HELLO put the password (or
  `user:password`) in the codec, keyed by a per-connection counter, and
  the statement carries only the counter under `x-resp.credential`.
  `take_credential` resolves it, forgets the entry and removes the key.
  No decode output holds the secret. The audit trail sees the handle; the
  Sidecar resolves the credential only on a lane with per-request
  identity. The codec keeps the last 16 unresolved entries.

## Build

Needs Rust 1.82+ and `rustup target add wasm32-unknown-unknown`. `Cargo.toml`
names the `hoop-codec` crate: a path dependency on `../../sdk/rust` inside the
hoop repository, the crates.io version in the copy under `hoophq/plugins`.

```bash
./build.sh     # dist/resp.wasm, copied to resp.wasm; about 160 KB
cargo test     # the decoder on the host, no wasm needed
```

`resp.wasm` is checked in beside `fixtures.json`, so the conformance run
below needs no Rust wasm target. The plug-in workflow rebuilds the module
from source on every change under this directory or the SDK and proves it
against the fixtures; the checked-in copy is what `make
test-sidecar-plugins` proves.

## Test

```bash
hoop start sidecar --codec-test resp.wasm fixtures.json
hoop start sidecar --validate --config config.yaml
```

Inside the hoop repository `make test-sidecar-plugins` runs the same checks
for every SDK and example with a freshly built `hoop-inspect`.

`-validate` resolves `module:` against the working directory, so run it
from here.

`fixtures.json` covers SET/GET/DEL/FLUSHALL, MULTI..EXEC and DISCARD,
MSET and EVAL keys, AUTH, inline commands, an inline line past
`max_inline_bytes` that arrived whole, a bulk string split across two
reads, a non-numeric array count, a control byte, server replies with a
nested array and an error, and a bad server type byte. All eight checks
pass.

## Run

`config.yaml` puts the lane on `127.0.0.1:16379` in front of a Redis on
`127.0.0.1:6379`:

```yaml
plugins:
  - protocol: x-resp
    module: ./resp.wasm

listeners:
  - name: cache
    protocol: x-resp
    listen: 127.0.0.1:16379
    upstream: 127.0.0.1:6379
    plugin:
      max_text_bytes: "4096"
    guardrails:
      rules:
        - name: no-flush
          type: metadata
          metadata:
            x-resp.command: [FLUSHALL, FLUSHDB]
          message: flushing the cache is not permitted through this lane
```

```bash
hoop-inspect -config config.yaml &
redis-cli -p 16379
127.0.0.1:16379> SET user:1 bob
OK
127.0.0.1:16379> GET user:1
"bob"
127.0.0.1:16379> FLUSHALL
(error) ERR flushing the cache is not permitted through this lane
```

The denied connection closes after the error; `redis-cli` reconnects on
the next command. Every command and reply lands in the audit stream as a
`statement` record, the denial as a `violation`.

## What each rule type can fence

| rule type | reads | example |
|---|---|---|
| `operation` | the mapped verb | `operations: [delete, truncate]` refuses DEL, UNLINK, HDEL, LPOP, RPOP, SREM, ZREM, FLUSHALL, FLUSHDB |
| `table` | the lowercased keys, with `access` | `tables: [feature_flags]`, `access: write` refuses SET, INCR, DEL and every other write on that key; `require_table_match: true` also refuses commands with no key |
| `metadata` | `x-resp.command`, `x-resp.key`, `x-resp.reply` | `x-resp.command: [CONFIG, DEBUG, HELLO]`; `x-resp.key: ["session:admin"]` |
| `pattern_match` | the command line | `pattern_regex: '^(?i)keys\s+\*'` refuses a full keyspace scan |
| `deny_words_list` | the command line | `words: [DEBUG]` |

Two notes on `table`: the matcher compares whole names, so a rule names
one key, not a prefix; and a key of the form `a.b` also matches a rule
for `b`, because the matcher treats the dot as a schema separator. Use
`metadata` on `x-resp.key` for an exact key.

The free tier accepts one guardrail rule per process; a license lifts the
cap.
