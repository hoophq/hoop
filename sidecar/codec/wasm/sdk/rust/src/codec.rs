//! The `Codec` trait a plug-in implements, and the per-connection registry
//! the exports dispatch through.

use std::cell::RefCell;
use std::collections::BTreeMap;

use crate::abi;
use crate::host;
use crate::types::*;

/// One connection's decoder. `export_codec!` turns an implementation into
/// the ABI exports; the host creates one value per connection through
/// `Default`, calls `open` with the listener's options, and drops it at
/// `close`.
///
/// `decode` is the one required method. The other methods back the
/// optional capabilities, and the export for each exists only when the
/// `export_codec!` list names the capability, so the host never reaches an
/// unlisted method.
pub trait Codec: Default {
    /// A connection begins. `options` is the listener's settings, keyed by
    /// the manifest's option names. An `Err` refuses the connection.
    fn open(&mut self, options: &BTreeMap<String, String>) -> Result<(), String> {
        let _ = options;
        Ok(())
    }

    /// Decodes `data`, which begins at a message boundary, into statements.
    ///
    /// `Decoded::consumed` must stop at the first byte of an incomplete
    /// trailing message; the host passes it again, prefixed to the next
    /// read. `Err` means the bytes are malformed for this protocol and the
    /// host drops the connection.
    fn decode(&mut self, dir: Direction, data: &[u8]) -> Result<Decoded, String>;

    /// Renders `message` as the protocol's native error frame, which the
    /// host sends before closing a denied connection. Capability `deny`.
    fn deny(&self, dir: Direction, message: &str) -> Vec<u8> {
        let _ = (dir, message);
        Vec::new()
    }

    /// Transforms `data` before inspection and forwarding. May hold a
    /// prefix and return nothing. Capability `filter`.
    fn filter(&mut self, dir: Direction, data: &[u8]) -> Vec<u8> {
        let _ = dir;
        data.to_vec()
    }

    /// The host enables response masking before any server bytes arrive,
    /// and only when the lane has a masker. Capability `rewrite`.
    fn enable_rewrite(&mut self) {}

    /// Receives every server chunk after `decode` saw it, hands each cell
    /// it can name to `mask(column, value)`, and returns the rebuilt
    /// frames. The codec may hold rows until it can rebuild them.
    /// Capability `rewrite`.
    fn rewrite(
        &mut self,
        data: &[u8],
        mask: &mut dyn FnMut(&str, &[u8]) -> Vec<u8>,
    ) -> Result<Rewritten, String> {
        let _ = mask;
        Ok(Rewritten { bytes: data.to_vec(), cells: 0, rows: 0 })
    }

    /// Releases every row `rewrite` held; called when the connection ends.
    /// Capability `rewrite`.
    fn flush(&mut self, mask: &mut dyn FnMut(&str, &[u8]) -> Vec<u8>) -> Result<Rewritten, String> {
        let _ = mask;
        Ok(Rewritten::default())
    }

    /// Lifts the credential out of a request statement and returns it with
    /// the statement scrubbed of every trace. `None` when the request
    /// carries none. Capability `credential`.
    fn take_credential(&mut self, stmt: Statement) -> Option<(String, Statement)> {
        let _ = stmt;
        None
    }

    /// Renders a statement for the AI analyzer. `None` means nothing to
    /// classify. The host bounds the text to the analyzer's budget.
    /// Capability `content`.
    fn content(&self, stmt: &Statement) -> Option<Content> {
        let _ = stmt;
        None
    }
}

/// The codecs of one instance, keyed by the `conn` the host passes.
///
/// Under `per_connection` instancing the map holds one entry, for
/// connection 0; under `per_lane` one entry per open connection. The
/// `RefCell` is enough synchronization: the ABI serializes every call into
/// an instance, and wasm32-unknown-unknown has no threads to race it. The
/// `Sync` impl below states that assumption, which is also why
/// `export_codec!` is the only intended user of this type.
pub struct Connections<C: Codec> {
    conns: RefCell<BTreeMap<u32, C>>,
}

// SAFETY: see the type's doc comment. A `static` needs `Sync`, and the
// host guarantees single-threaded, serialized access.
unsafe impl<C: Codec> Sync for Connections<C> {}

impl<C: Codec> Default for Connections<C> {
    fn default() -> Self {
        Connections::new()
    }
}

impl<C: Codec> Connections<C> {
    pub const fn new() -> Connections<C> {
        Connections { conns: RefCell::new(BTreeMap::new()) }
    }

    /// The `open` export. Non-zero refuses the connection: 1 for an
    /// options object the SDK cannot read, 2 for a codec that refused.
    pub fn open(&self, conn: u32, options_json: &[u8]) -> u32 {
        let options: BTreeMap<String, String> = if options_json.is_empty() {
            BTreeMap::new()
        } else {
            match serde_json::from_slice(options_json) {
                Ok(o) => o,
                Err(err) => {
                    host::log(host::Level::Error, &format!("hoop-codec: options for connection {conn} are not a string map: {err}"));
                    return 1;
                }
            }
        };
        let mut codec = C::default();
        if let Err(reason) = codec.open(&options) {
            host::log(host::Level::Error, &format!("hoop-codec: connection {conn} refused: {reason}"));
            return 2;
        }
        self.conns.borrow_mut().insert(conn, codec);
        0
    }

    /// The `close` export. Drops the codec; a second close is a no-op.
    pub fn close(&self, conn: u32) {
        self.conns.borrow_mut().remove(&conn);
    }

    /// The `decode` export; returns DecodeResult JSON.
    pub fn decode(&self, conn: u32, dir: u32, data: &[u8]) -> Vec<u8> {
        let dir = Direction::from_abi(dir);
        let mut conns = self.conns.borrow_mut();
        let Some(codec) = conns.get_mut(&conn) else {
            // A decode on a connection the host never opened is a host
            // bug. Reporting it as a decode error drops that connection
            // alone, which is the proportionate failure under per_lane.
            return error_json(&format!("hoop-codec: connection {conn} was not opened"));
        };
        match codec.decode(dir, data) {
            Ok(decoded) => serde_json::to_vec(&DecodeResult {
                statements: &decoded.statements,
                consumed: decoded.consumed as u64,
            })
            .expect("hoop-codec: a Statement always serializes"),
            Err(err) => error_json(&err),
        }
    }

    /// The `deny` export; returns raw frame bytes.
    pub fn deny(&self, conn: u32, dir: u32, message: &[u8]) -> Vec<u8> {
        let dir = Direction::from_abi(dir);
        let message = String::from_utf8_lossy(message);
        self.with(conn, "deny", |codec| codec.deny(dir, &message))
    }

    /// The `filter` export; returns raw bytes.
    pub fn filter(&self, conn: u32, dir: u32, data: &[u8]) -> Vec<u8> {
        let dir = Direction::from_abi(dir);
        self.with(conn, "filter", |codec| codec.filter(dir, data))
    }

    /// The `enable_rewrite` export.
    pub fn enable_rewrite(&self, conn: u32) {
        self.with(conn, "enable_rewrite", |codec| codec.enable_rewrite())
    }

    /// The `rewrite` export; returns RewriteResult JSON. The mask closure
    /// reaches the host's `mask` import, legal only on this stack.
    pub fn rewrite(&self, conn: u32, data: &[u8]) -> Vec<u8> {
        self.with(conn, "rewrite", |codec| rewrite_json(codec.rewrite(data, &mut host::mask)))
    }

    /// The `flush` export; returns RewriteResult JSON.
    pub fn flush(&self, conn: u32) -> Vec<u8> {
        self.with(conn, "flush", |codec| rewrite_json(codec.flush(&mut host::mask)))
    }

    /// The `take_credential` export; returns CredentialResult JSON.
    pub fn take_credential(&self, conn: u32, stmt_json: &[u8]) -> Vec<u8> {
        let stmt = parse_statement(stmt_json);
        self.with(conn, "take_credential", |codec| {
            let result = match codec.take_credential(stmt) {
                Some((credential, scrubbed)) => serde_json::to_vec(&CredentialResult {
                    ok: true,
                    credential: Some(&credential),
                    statement: Some(&scrubbed),
                }),
                None => serde_json::to_vec(&CredentialResult { ok: false, credential: None, statement: None }),
            };
            result.expect("hoop-codec: a CredentialResult always serializes")
        })
    }

    /// The `content` export; returns ContentResult JSON.
    pub fn content(&self, conn: u32, stmt_json: &[u8]) -> Vec<u8> {
        let stmt = parse_statement(stmt_json);
        self.with(conn, "content", |codec| {
            let result = match codec.content(&stmt) {
                Some(content) => serde_json::to_vec(&ContentResult {
                    ok: true,
                    text: Some(&content.text),
                    cache_key: Some(&content.cache_key),
                }),
                None => serde_json::to_vec(&ContentResult { ok: false, text: None, cache_key: None }),
            };
            result.expect("hoop-codec: a ContentResult always serializes")
        })
    }

    /// Runs `f` on the codec of `conn`. The capability exports have no
    /// error channel, so a connection the host never opened traps: the
    /// host reports the failure and fails that stream closed.
    fn with<R>(&self, conn: u32, export: &str, f: impl FnOnce(&mut C) -> R) -> R {
        let mut conns = self.conns.borrow_mut();
        match conns.get_mut(&conn) {
            Some(codec) => f(codec),
            None => panic!("hoop-codec: {export} on connection {conn}, which was not opened"),
        }
    }

    /// The count of open connections; for tests.
    pub fn len(&self) -> usize {
        self.conns.borrow().len()
    }

    pub fn is_empty(&self) -> bool {
        self.len() == 0
    }
}

/// Renders the manifest for `describe`, holding the capability list to the
/// exports the macro emitted. A mismatch traps, which refuses the module at
/// load: the host would refuse it anyway, and this names the cause.
pub fn describe(manifest: &Manifest, exported: &[Capability]) -> Vec<u8> {
    if manifest.abi != ABI_VERSION {
        panic!("hoop-codec: manifest abi {} but this SDK speaks {ABI_VERSION}", manifest.abi);
    }
    let mut declared = manifest.capabilities.clone();
    declared.sort();
    declared.dedup();
    let mut emitted = exported.to_vec();
    emitted.sort();
    emitted.dedup();
    if declared != emitted {
        panic!(
            "hoop-codec: manifest capabilities {declared:?} but export_codec! lists {emitted:?}; the two must name the same set"
        );
    }
    serde_json::to_vec(manifest).expect("hoop-codec: a Manifest always serializes")
}

fn parse_statement(stmt_json: &[u8]) -> Statement {
    // The host wrote it from its own Statement, so a parse failure is a
    // version skew between host and SDK.
    serde_json::from_slice(stmt_json)
        .unwrap_or_else(|err| panic!("hoop-codec: the host passed a Statement this SDK cannot read: {err}"))
}

fn error_json(message: &str) -> Vec<u8> {
    serde_json::to_vec(&ErrorResult { error: message }).expect("hoop-codec: an ErrorResult always serializes")
}

fn rewrite_json(result: Result<Rewritten, String>) -> Vec<u8> {
    match result {
        Ok(r) => serde_json::to_vec(&RewriteResult { bytes: abi::base64(&r.bytes), cells: r.cells, rows: r.rows })
            .expect("hoop-codec: a RewriteResult always serializes"),
        Err(err) => error_json(&err),
    }
}
