# hoop-codec

The Rust SDK for [hoop](https://hoop.dev) codec plug-ins. A plug-in is one
`cdylib` crate built for `wasm32-unknown-unknown`: it implements `Codec`
for a type, describes itself in a `Manifest`, and calls `export_codec!`
once at the crate root. The macro emits every export of the plug-in ABI;
the Sidecar instantiates the module, feeds it the bytes of one connection,
and reads back the statements its policy evaluates.

The full guide is at
<https://hoop.dev/docs/setup/configuration/hoop-sidecar/plugins/writing>.
The ABI the crate speaks is `sidecar/codec/wasm/abi/ABI.md` in the
[hoop repository](https://github.com/hoophq/hoop); the `x-acmewire`
example under `sidecar/codec/wasm/sdk/rust/examples/acmewire` is a
complete plug-in that exercises every capability.

## Install

```sh
rustup target add wasm32-unknown-unknown
cargo add hoop-codec
```

The plug-in crate is a `cdylib`, and its release profile turns a panic
into the trap the ABI expects:

```toml
[lib]
crate-type = ["cdylib"]

[profile.release]
opt-level = "s"
lto = true
codegen-units = 1
panic = "abort"
strip = true
```

## Example

```rust
use hoop_codec::{host, Capability, Codec, Decoded, Direction, Manifest, Operation, SqlDialect, Statement};

pub fn manifest() -> Manifest {
    let mut m = Manifest::new("x-acmewire", "Acme Wire");
    m.version = "0.1.0".into();
    m.capabilities = vec![Capability::Deny];
    m.sql_dialect = Some(SqlDialect::Mysql);
    m
}

hoop_codec::export_codec!(AcmeWire, manifest, [deny]);

#[derive(Default)]
pub struct AcmeWire;

impl Codec for AcmeWire {
    fn decode(&mut self, dir: Direction, data: &[u8]) -> Result<Decoded, String> {
        let mut out = Decoded::default();
        let mut pos = 0;
        while let Some((opcode, payload, len)) = frame(&data[pos..])? {
            pos += len;
            if dir == Direction::Client && opcode == b'Q' {
                let sql = String::from_utf8_lossy(payload).into_owned();
                let stmt = Statement::new(Operation::Unknown, &sql)
                    .with_analysis(host::analyze_sql(&sql))
                    .with_metadata("x-acmewire.verb", "QUERY");
                out.statements.push(stmt);
            }
        }
        out.consumed = pos;
        Ok(out)
    }

    fn deny(&self, _dir: Direction, message: &str) -> Vec<u8> {
        error_frame(message)
    }
}
```

`frame` and `error_frame` are yours: one reads a frame header and payload
from the front of `data` and returns `Ok(None)` when the frame is
incomplete, so `consumed` stops at its first byte; the other renders the
protocol's native error. The capability list in `export_codec!` and
`manifest.capabilities` must name the same set; `describe` traps when
they differ.

The optional methods (`filter`, `rewrite`, `flush`, `take_credential`,
`content`, `open`) have defaults. Add one at a time and list its
capability when you do.

## Build and test

```sh
cargo build --release --target wasm32-unknown-unknown
cargo test                        # runs on the host, no wasm needed
```

Prove the module against the ABI, then against your own fixtures:

```sh
hoop start sidecar --codec-test target/wasm32-unknown-unknown/release/acmewire.wasm acmewire.fixtures.json
```

## Releasing

One PR bumps `sidecar/codec/wasm/sdk/VERSION` and `version` here to the
same number; CI refuses the PR when they differ. The merge to main tags
`hoop-codec-vX.Y.Z`, and from that tag the release workflow publishes
this crate, tags the Go module `sidecar/codec/wasm/sdk/tinygo/vX.Y.Z`,
attaches `hoop_codec.h` to the GitHub release and opens the PR that pins
the examples in hoophq/plugins. No tag is pushed by hand.

## License

MIT. See `LICENSE`.
