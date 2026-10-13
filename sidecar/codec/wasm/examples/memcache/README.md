# x-memcache: the memcached text protocol in Go (TinyGo)

A hoop Sidecar codec plug-in for the [memcached text protocol](https://github.com/memcached/memcached/blob/master/doc/protocol.txt), written against the TinyGo SDK (`../../sdk/tinygo`). It shows a line protocol with data blocks, a native deny frame, and custom content for the AI analyzer.

| | |
|---|---|
| Protocol | `x-memcache`, label "Memcached text" |
| Capabilities | `deny`, `content` |
| Instances | `per_connection` |
| Option | `max_line_bytes` (int, default `2048`): the longest command or reply line before the connection is dropped as malformed |
| Files | `main.go` manifest, `memcache.go` decoder, `memcache_test.go` host-side tests, `fixtures.json` conformance scripts |

## The protocol

Every client command is one line ending in `\r\n`. Storage commands carry a data block behind the line, framed by the `<bytes>` token; the block ends in `\r\n` too:

```
set <key> <flags> <exptime> <bytes> [noreply]\r\n<data>\r\n
cas <key> <flags> <exptime> <bytes> <cas unique> [noreply]\r\n<data>\r\n
get <key>+\r\n
delete <key> [noreply]\r\n
incr <key> <n> [noreply]\r\n
touch <key> <exptime> [noreply]\r\n
flush_all [delay] [noreply]\r\n
stats\r\n   version\r\n   verbosity <n>\r\n   quit\r\n
```

The server answers one line per command, or a `VALUE <key> <flags> <bytes>\r\n<data>\r\n` block per hit followed by `END\r\n` for a `get`.

### What the decoder returns

One statement per client line. The data block belongs to the statement but is not its text.

| Command | `operation` | `tables` / `relations` |
|---|---|---|
| `get`, `gets` | `select` | every key, access `read` |
| `set`, `add`, `replace`, `append`, `prepend`, `cas` | `update` | the key, access `write` |
| `incr`, `decr`, `touch` | `update` | the key, access `write` |
| `delete` | `delete` | the key, access `write` |
| `flush_all` | `truncate` | none |
| `stats`, `version` | `show` | none |
| `verbosity` | `set` | none |
| `quit`, anything else | `other` | none |

Key names are lowercased in `tables` and `relations`; the policy compares them as they arrive. Metadata carries the native facts:

| Key | Value |
|---|---|
| `x-memcache.command` | the command word as the client wrote it |
| `x-memcache.key` | the first key, as written |
| `x-memcache.noreply` | `"true"` when the line ends in `noreply` on a command that accepts it |
| `x-memcache.reply` | on a server statement, the first word of the reply line (`STORED`, `VALUE`, `END`, `SERVER_ERROR`, the number an `incr` returns, ...) |

A line memcached answers with `ERROR` is not a decode error: an unknown word (`GET`, the meta commands `mg`/`ms`/`md`, `gat`, `slabs`, ...), a known word short of its arguments, or a storage line with a token past `<bytes>` (or `<cas unique>`) that is not `noreply` is one statement of operation `other` with the line as text and the word in `x-memcache.command`. memcached executes nothing for such a line and reads no data block, so the decoder reads none either: the bytes behind `set k 0 0 3 bogus` are the next command line.

Two inputs are malformed and drop the connection: a line longer than `max_line_bytes` without `\r\n`, and a storage command (or a `VALUE` reply) whose `<bytes>` is not a number. A `VALUE` block is one server statement with `result.row_count` 1; `END` is its own statement.

A denial travels as `SERVER_ERROR <message>\r\n`, which every memcached client already parses, and the connection closes behind it.

## Build

The module needs TinyGo 0.33 or later and `wasm-opt` from binaryen, which the TinyGo formula installs:

```sh
brew tap tinygo-org/tools && brew install tinygo
./build.sh                       # writes dist/memcache.wasm and copies it to memcache.wasm
```

`build.sh` runs:

```sh
tinygo build -o dist/memcache.wasm -target wasm-unknown -opt=z -no-debug \
  -tags hoop_deny,hoop_content .
```

The `-tags` list and the `Capabilities` in `main.go` must name the same set: the SDK's `describe` traps at load when they differ. `go.mod` names the SDK module `github.com/hoophq/hoop/sidecar/codec/wasm/sdk/tinygo`: through a `replace` to `../../sdk/tinygo` inside the hoop repository, by its tagged version in the copy under `hoophq/plugins`.

`build.sh` refuses to run without `tinygo` in `PATH` and names the install command. A TinyGo release tarball works too; it needs `wasm-opt` in `PATH` or named by `WASMOPT`.

`memcache.wasm` is checked in beside `fixtures.json`, so the conformance run below needs no TinyGo. The plug-in workflow rebuilds the module from source on every change under this directory or the SDK and proves it against the fixtures; the checked-in copy is what `make test-sidecar-plugins` proves.

### Go commands

Inside the hoop repository this module is absent from `go.work`, like the SDK: the workspace pins a Go version TinyGo does not follow, and `make test-sidecar` filters its module walk against `go.work`, so `make test-sidecar-plugins` runs its tests instead. Under the workspace `go` refuses to work here, so every `go` command needs `GOWORK=off`. Outside the repository the plain commands work:

```sh
GOWORK=off go vet -tags hoop_deny,hoop_content ./...
GOWORK=off go test ./...
```

## Test

`memcache_test.go` runs on the host with the standard toolchain, no wasm needed. It reads `fixtures.json` and replays every script through `Decode` twice: whole, and one byte at a time through a reassembly loop that retains `data[consumed:]` the way the Sidecar does. The two must agree, and both must match the fixture expectations. Deny, Content, the option and the line limit have their own tests.

The conformance tool proves the built module against the ABI and the same fixtures:

```sh
hoop start sidecar --codec-test memcache.wasm fixtures.json
```

Inside the hoop repository `make test-sidecar-plugins` runs the same check for every SDK and example with a freshly built `hoop-inspect`.

```
check result detail
1     ok     describe: x-memcache "Memcached text" v0.1.0, capabilities [deny content], instances per_connection, sql_dialect ""
2     ok     decode of zero bytes: nothing consumed, nothing returned, both directions
3     ok     deny: client 32 bytes, server 32 bytes
5     ok     garbage input returned without a hang or a panic; client: error "x-memcache: line longer than 2048 bytes without CRLF"; server: error "x-memcache: line longer than 2048 bytes without CRLF"
4     ok     operations: 47 statement(s), every operation listed by inspect.Operations
6     ok     11 script(s) replayed whole
7     ok     11 script(s) replayed one byte at a time match the whole replay
8     ok     11 script pair(s) interleaved on two connections match their solo replays
```

`fixtures.json` covers each operation class, a data block split across chunks (a partial frame consumes nothing), a `VALUE` block split across chunks, unknown and incomplete commands, a storage line with a token that is not `noreply` followed by what would have been its data block, the `max_line_bytes` option, and the two malformed inputs.

## Run

`config.yaml` puts a lane on `127.0.0.1:21211` in front of a memcached on `127.0.0.1:11211` and refuses `flush_all`:

```yaml
plugins:
  - protocol: x-memcache
    module: ./memcache.wasm

listeners:
  - name: cache
    protocol: x-memcache
    listen: 127.0.0.1:21211
    upstream: 127.0.0.1:11211
    plugin:
      max_line_bytes: "2048"
    guardrails:
      rules:
        - name: no-flush
          type: metadata
          metadata:
            x-memcache.command: [flush_all]
          message: flush_all is refused on this lane
```

```sh
hoop start sidecar --config config.yaml --validate
hoop start sidecar --config config.yaml
```

A session through the lane, with `nc 127.0.0.1 21211` (or `telnet`); `>` is what you type:

```
> set user:1 0 900 5
> hello
STORED
> get user:1
VALUE user:1 0 5
hello
END
> incr counter 1
NOT_FOUND
> delete user:1
DELETED
> version
VERSION 1.6.45
> flush_all
SERVER_ERROR flush_all is refused on this lane
```

The last reply is the plug-in's deny frame; the Sidecar closes the connection behind it. The audit log carries one `statement` event per line in both directions and one `violation` event for the denied `flush_all`.

## What each rule type can fence

| Rule type | On this lane |
|---|---|
| `operation` | `select`, `update`, `delete`, `truncate`, `show`, `set`, `other`. `operations: [update, delete, truncate]` makes the lane read-only; add `other` to refuse what memcached would answer `ERROR` to. |
| `table` | keys, lowercased, by name; a key prefix such as `session:` needs `pattern_match` on the line. `access: write` matches the storage commands, `incr`/`decr`/`touch` and `delete`; `access: read` matches `get`/`gets`. |
| `metadata` | `x-memcache.command` for a command word (`flush_all`, `stats`, `mg`), `x-memcache.key` for an exact key, `x-memcache.noreply` to refuse fire-and-forget writes, `x-memcache.reply` on responses. |
| `deny_words_list`, `pattern_match` | the command line: the word, the keys and the arguments. The data block is not in the text, so a pattern never sees a stored value. |
| `pii` | the command line only, for the same reason. |

A second rule worth keeping beside `no-flush` once the lane has a license for more than one:

```yaml
        - name: no-stats
          type: metadata
          metadata:
            x-memcache.command: [stats]
          message: stats are not readable through this lane
```

`http_*` and `grpc_status` rules read fields this protocol never produces and match nothing.
