//! x-acmewire: a made-up database wire protocol, written as the template
//! for a Rust codec plug-in and as the fixture the host's conformance
//! tests replay (`sidecar/codec/wasm/testdata/acmewire.*`).
//!
//! Every frame is `opcode u8, length u32 big-endian, payload`. The
//! client sends `Q` (SQL text), `P` (purge a table) and `A` (a bearer
//! token). The server answers with `C` (column names separated by 0x00),
//! `D` (one row: cells of `u32 length + bytes`), `R` (a `u32` row count)
//! and `E` (an error message). Either side may pad with 0x00 bytes between
//! frames as a keepalive. The example exercises every capability of the
//! ABI: `deny` renders an `E` frame, `filter` strips the padding, `rewrite`
//! masks `D` cells, `credential` trades the handle an `A` frame left on its
//! statement for the token, `content` renders for the analyzer.

use std::collections::BTreeMap;

use hoop_codec::{
    host, Access, Capability, Codec, Column, Content, Decoded, Direction, Instances, Manifest, Operation,
    OptionSpec, OptionType, Relation, ResultDetail, Rewritten, SqlDialect, Statement,
};

const PROTOCOL: &str = "x-acmewire";
const VERB_KEY: &str = "x-acmewire.verb";
const CREDENTIAL_KEY: &str = "x-acmewire.credential";
const DEFAULT_DENY_PREFIX: &str = "ACME";
const HEADER_LEN: usize = 5;
/// Bound on tokens lifted and not yet taken. The host trades each
/// handle back only on a lane with per-request identity; elsewhere every
/// `A` frame would otherwise hold one more token for the life of the
/// connection.
const MAX_HELD_CREDENTIALS: usize = 1024;

pub fn manifest() -> Manifest {
    let mut m = Manifest::new(PROTOCOL, "Acme Wire");
    m.version = "0.1.0".into();
    m.capabilities = vec![
        Capability::Deny,
        Capability::Filter,
        Capability::Rewrite,
        Capability::Credential,
        Capability::Content,
    ];
    m.sql_dialect = Some(SqlDialect::Mysql);
    m.instances = Some(Instances::PerConnection);
    m.options = vec![OptionSpec {
        name: "deny_prefix".into(),
        label: Some("Deny prefix".into()),
        kind: OptionType::String,
        values: Vec::new(),
        default: Some(DEFAULT_DENY_PREFIX.into()),
        help: Some("Prefix of the error message a denied client receives.".into()),
    }];
    m
}

hoop_codec::export_codec!(AcmeWire, manifest, [deny, filter, rewrite, credential, content]);

/// One connection. The filter keeps a frame cursor per direction so it can
/// tell padding from payload bytes that happen to be zero; the rewrite
/// path keeps its own buffer and column list because it frames the server
/// stream a second time, on the chunks the host forwards. Tokens stay
/// here, keyed by the handle `decode` put on the statement: audit, policy
/// and the analyzer see the statement, so the token must not be on it.
#[derive(Default)]
pub struct AcmeWire {
    deny_prefix: String,
    filter: [FrameCursor; 2],
    rewrite_buf: Vec<u8>,
    columns: Vec<String>,
    credentials: BTreeMap<String, String>,
    next_handle: u64,
}

/// The filter's position in the current frame of one direction.
#[derive(Default)]
struct FrameCursor {
    /// Header bytes seen so far, fewer than HEADER_LEN.
    header: Vec<u8>,
    /// Payload bytes of the current frame still to pass through.
    remaining: usize,
}

/// A complete frame at the front of a buffer.
struct Frame<'a> {
    opcode: u8,
    payload: &'a [u8],
}

impl Frame<'_> {
    fn size(&self) -> usize {
        HEADER_LEN + self.payload.len()
    }
}

/// Splits one frame off the front of `data`. `None` means the frame is
/// incomplete, which is the host's cue to buffer and call again.
fn frame(data: &[u8]) -> Option<Frame<'_>> {
    if data.len() < HEADER_LEN {
        return None;
    }
    let len = u32::from_be_bytes([data[1], data[2], data[3], data[4]]) as usize;
    let end = HEADER_LEN.checked_add(len)?;
    if data.len() < end {
        return None;
    }
    Some(Frame { opcode: data[0], payload: &data[HEADER_LEN..end] })
}

fn encode(opcode: u8, payload: &[u8]) -> Vec<u8> {
    let mut out = Vec::with_capacity(HEADER_LEN + payload.len());
    out.push(opcode);
    out.extend_from_slice(&(payload.len() as u32).to_be_bytes());
    out.extend_from_slice(payload);
    out
}

/// The opcodes each side may send. The codec checks the first byte, so a
/// bogus opcode with a huge length fails now instead of after the host
/// buffered `max_reassembly` bytes waiting for it.
fn known(dir: Direction, opcode: u8) -> bool {
    match dir {
        Direction::Client => matches!(opcode, b'Q' | b'P' | b'A'),
        Direction::Server => matches!(opcode, b'C' | b'D' | b'R' | b'E'),
    }
}

/// Splits a `D` payload into its cells.
fn cells(payload: &[u8]) -> Result<Vec<&[u8]>, String> {
    let mut cells = Vec::new();
    let mut rest = payload;
    while !rest.is_empty() {
        if rest.len() < 4 {
            return Err("x-acmewire: row cell header is truncated".into());
        }
        let len = u32::from_be_bytes([rest[0], rest[1], rest[2], rest[3]]) as usize;
        let cell = rest.get(4..4 + len).ok_or("x-acmewire: row cell overruns its frame")?;
        cells.push(cell);
        rest = &rest[4 + len..];
    }
    Ok(cells)
}

fn text(payload: &[u8]) -> String {
    String::from_utf8_lossy(payload).into_owned()
}

impl AcmeWire {
    fn query(&self, payload: &[u8]) -> Vec<Statement> {
        let sql = text(payload);
        let mut parts = host::split_sql(&sql);
        if parts.is_empty() {
            parts.push(sql);
        }
        parts
            .into_iter()
            .map(|part| {
                let analysis = host::analyze_sql(&part);
                Statement::new(Operation::Unknown, part).with_analysis(analysis).with_metadata(VERB_KEY, "QUERY")
            })
            .collect()
    }

    fn purge(&self, payload: &[u8]) -> Statement {
        let name = text(payload);
        let table = name.to_lowercase();
        let mut stmt = Statement::new(Operation::Delete, format!("PURGE {name}")).with_metadata(VERB_KEY, "PURGE");
        stmt.effects = vec![Operation::Delete];
        stmt.relations = vec![Relation { name: table.clone(), access: Access::Write }];
        stmt.tables = vec![table];
        stmt
    }

    fn auth(&mut self, payload: &[u8]) -> Result<Statement, String> {
        if self.credentials.len() >= MAX_HELD_CREDENTIALS {
            return Err(format!("x-acmewire: {MAX_HELD_CREDENTIALS} credentials were lifted and never taken"));
        }
        self.next_handle += 1;
        let handle = self.next_handle.to_string();
        self.credentials.insert(handle.clone(), text(payload));
        Ok(Statement::new(Operation::Other, "AUTH").with_metadata(VERB_KEY, "AUTH").with_metadata(CREDENTIAL_KEY, handle))
    }

    fn column_names(&self, payload: &[u8]) -> Statement {
        let columns: Vec<Column> = if payload.is_empty() {
            Vec::new()
        } else {
            payload.split(|b| *b == 0).map(|name| Column::named(text(name))).collect()
        };
        Statement::new(Operation::Other, "COLUMNS")
            .with_metadata(VERB_KEY, "COLUMNS")
            .with_result(ResultDetail { columns, ..ResultDetail::default() })
    }

    fn row(&self, payload: &[u8]) -> Result<Statement, String> {
        cells(payload)?;
        Ok(Statement::new(Operation::Other, "ROW")
            .with_metadata(VERB_KEY, "ROW")
            .with_result(ResultDetail { row_count: 1, ..ResultDetail::default() }))
    }

    fn done(&self, payload: &[u8]) -> Result<Statement, String> {
        let count: [u8; 4] = payload.try_into().map_err(|_| "x-acmewire: R frame carries no u32 row count")?;
        Ok(Statement::new(Operation::Other, "DONE")
            .with_metadata(VERB_KEY, "DONE")
            .with_result(ResultDetail { row_count: u32::from_be_bytes(count) as u64, ..ResultDetail::default() }))
    }

    fn error(&self, payload: &[u8]) -> Statement {
        Statement::new(Operation::Other, format!("ERROR: {}", text(payload))).with_metadata(VERB_KEY, "ERROR")
    }

    /// Masks every cell of a `D` frame and re-encodes the lengths. The
    /// count is of cells `mask` changed, which is what the audit trail
    /// reports as masked; a cell handed over and returned as-is is not.
    fn mask_row(&self, payload: &[u8], mask: &mut dyn FnMut(&str, &[u8]) -> Vec<u8>) -> Result<(Vec<u8>, u64), String> {
        let mut out = Vec::with_capacity(payload.len());
        let mut changed = 0;
        for (i, cell) in cells(payload)?.into_iter().enumerate() {
            let column = self.columns.get(i).map(String::as_str).unwrap_or("");
            let masked = mask(column, cell);
            if masked != cell {
                changed += 1;
            }
            out.extend_from_slice(&(masked.len() as u32).to_be_bytes());
            out.extend_from_slice(&masked);
        }
        Ok((out, changed))
    }
}

impl Codec for AcmeWire {
    fn open(&mut self, options: &BTreeMap<String, String>) -> Result<(), String> {
        self.deny_prefix = options.get("deny_prefix").cloned().unwrap_or_else(|| DEFAULT_DENY_PREFIX.into());
        if self.deny_prefix.is_empty() {
            return Err("x-acmewire: deny_prefix must not be empty".into());
        }
        Ok(())
    }

    fn decode(&mut self, dir: Direction, data: &[u8]) -> Result<Decoded, String> {
        let mut out = Decoded::empty();
        while out.consumed < data.len() {
            let rest = &data[out.consumed..];
            if !known(dir, rest[0]) {
                return Err(format!("x-acmewire: unknown opcode 0x{:02x} from the {:?}", rest[0], dir));
            }
            let Some(frame) = frame(rest) else { break };
            let stmts = match frame.opcode {
                b'Q' => self.query(frame.payload),
                b'P' => vec![self.purge(frame.payload)],
                b'A' => vec![self.auth(frame.payload)?],
                b'C' => vec![self.column_names(frame.payload)],
                b'D' => vec![self.row(frame.payload)?],
                b'R' => vec![self.done(frame.payload)?],
                b'E' => vec![self.error(frame.payload)],
                _ => unreachable!("known() admits only the opcodes above"),
            };
            out.statements.extend(stmts);
            out.consumed += frame.size();
        }
        Ok(out)
    }

    fn deny(&self, _dir: Direction, message: &str) -> Vec<u8> {
        encode(b'E', format!("{}: {message}", self.deny_prefix).as_bytes())
    }

    /// Drops 0x00 keepalive padding wherever a frame boundary is, under
    /// any chunking: the cursor remembers how much of the current frame is
    /// still to come, so a zero inside a payload passes through and a
    /// zero between frames does not, whether the two arrive in one read
    /// or one byte at a time. The cursor holds up to four header bytes
    /// until it knows the length.
    fn filter(&mut self, dir: Direction, data: &[u8]) -> Vec<u8> {
        let cursor = &mut self.filter[dir as usize];
        let mut out = Vec::with_capacity(data.len());
        let mut i = 0;
        while i < data.len() {
            if cursor.remaining > 0 {
                let n = cursor.remaining.min(data.len() - i);
                out.extend_from_slice(&data[i..i + n]);
                cursor.remaining -= n;
                i += n;
                continue;
            }
            if cursor.header.is_empty() && data[i] == 0 {
                i += 1;
                continue;
            }
            cursor.header.push(data[i]);
            i += 1;
            if cursor.header.len() == HEADER_LEN {
                let h = &cursor.header;
                cursor.remaining = u32::from_be_bytes([h[1], h[2], h[3], h[4]]) as usize;
                out.extend_from_slice(h);
                cursor.header.clear();
            }
        }
        out
    }

    /// Nothing to prepare: the column list fills as `C` frames pass
    /// through `rewrite`.
    fn enable_rewrite(&mut self) {}

    /// Rebuilds every complete `D` frame with masked cells and forwards the
    /// other frames untouched. A frame split across chunks waits in
    /// `rewrite_buf` for its tail; the buffer holds nothing once the frame
    /// is complete.
    fn rewrite(&mut self, data: &[u8], mask: &mut dyn FnMut(&str, &[u8]) -> Vec<u8>) -> Result<Rewritten, String> {
        self.rewrite_buf.extend_from_slice(data);
        let mut out = Rewritten::default();
        let mut consumed = 0;
        while let Some(frame) = frame(&self.rewrite_buf[consumed..]) {
            match frame.opcode {
                b'C' => {
                    self.columns = if frame.payload.is_empty() {
                        Vec::new()
                    } else {
                        frame.payload.split(|b| *b == 0).map(text).collect()
                    };
                    out.bytes.extend_from_slice(&self.rewrite_buf[consumed..consumed + frame.size()]);
                }
                b'D' => {
                    let (payload, changed) = self.mask_row(frame.payload, mask)?;
                    out.bytes.extend_from_slice(&encode(b'D', &payload));
                    out.cells += changed;
                    if changed > 0 {
                        out.rows += 1;
                    }
                }
                _ => out.bytes.extend_from_slice(&self.rewrite_buf[consumed..consumed + frame.size()]),
            }
            consumed += frame.size();
        }
        self.rewrite_buf.drain(..consumed);
        Ok(out)
    }

    /// The buffer holds at most one incomplete frame, and a frame without
    /// its tail cannot be rebuilt: forwarding it would leak the cells
    /// `mask` never saw. The connection is ending, so `flush` drops the
    /// fragment.
    fn flush(&mut self, _mask: &mut dyn FnMut(&str, &[u8]) -> Vec<u8>) -> Result<Rewritten, String> {
        self.rewrite_buf.clear();
        Ok(Rewritten::default())
    }

    /// Trades the handle for the token and forgets it, so the token is
    /// handed out once; a handle this connection never issued, or one
    /// already taken, is `None`.
    fn take_credential(&mut self, mut stmt: Statement) -> Option<(String, Statement)> {
        let handle = stmt.metadata.remove(CREDENTIAL_KEY)?;
        let token = self.credentials.remove(&handle)?;
        Some((token, stmt))
    }

    fn content(&self, stmt: &Statement) -> Option<Content> {
        if stmt.direction == Some(Direction::Server) {
            return None;
        }
        let text = stmt.text.trim();
        if text.is_empty() {
            return None;
        }
        let verb = stmt.metadata.get(VERB_KEY).map(String::as_str).unwrap_or("");
        let shape: String = text.to_lowercase().chars().filter(|c| !c.is_ascii_digit()).collect();
        Some(Content {
            text: format!("Protocol: {PROTOCOL}\nVerb: {verb}\n\n{text}"),
            cache_key: format!("{verb}|{shape}"),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn opened() -> AcmeWire {
        let mut c = AcmeWire::default();
        c.open(&BTreeMap::new()).unwrap();
        c
    }

    fn row_frame(cells: &[&[u8]]) -> Vec<u8> {
        let mut payload = Vec::new();
        for c in cells {
            payload.extend_from_slice(&(c.len() as u32).to_be_bytes());
            payload.extend_from_slice(c);
        }
        encode(b'D', &payload)
    }

    #[test]
    fn purge_is_a_delete_on_the_lowercased_table() {
        let mut c = opened();
        let d = c.decode(Direction::Client, &encode(b'P', b"Orders")).unwrap();
        assert_eq!(d.consumed, 11);
        let s = &d.statements[0];
        assert_eq!(s.operation, Operation::Delete);
        assert_eq!(s.text, "PURGE Orders");
        assert_eq!(s.tables, vec!["orders"]);
        assert_eq!(s.relations, vec![Relation { name: "orders".into(), access: Access::Write }]);
        assert_eq!(s.metadata[VERB_KEY], "PURGE");
    }

    #[test]
    fn partial_frame_consumes_nothing_and_bad_opcode_fails_at_once() {
        let mut c = opened();
        let mut partial = encode(b'P', b"orders");
        partial.truncate(8);
        let d = c.decode(Direction::Client, &partial).unwrap();
        assert_eq!((d.consumed, d.statements.len()), (0, 0));

        // The whole frame plus a dangling header: one statement, stop
        // before the header.
        let mut two = encode(b'P', b"orders");
        two.extend_from_slice(&[b'A', 0, 0]);
        let d = c.decode(Direction::Client, &two).unwrap();
        assert_eq!((d.consumed, d.statements.len()), (11, 1));

        assert!(c.decode(Direction::Client, b"X").is_err(), "the codec judges the opcode before the length arrives");
        assert!(c.decode(Direction::Client, &encode(b'C', b"")).is_err(), "a server opcode from the client");
    }

    #[test]
    fn server_frames_report_result_shape() {
        let mut c = opened();
        let mut bytes = encode(b'C', b"id\0email");
        bytes.extend(row_frame(&[b"1", b"a@b"]));
        bytes.extend(encode(b'R', &3u32.to_be_bytes()));
        bytes.extend(encode(b'E', b"boom"));
        let d = c.decode(Direction::Server, &bytes).unwrap();
        assert_eq!(d.consumed, bytes.len());
        let r = |i: usize| d.statements[i].result.clone().unwrap();
        assert_eq!(r(0).columns, vec![Column::named("id"), Column::named("email")]);
        assert_eq!(r(1).row_count, 1);
        assert_eq!(r(2).row_count, 3);
        assert_eq!(d.statements[3].text, "ERROR: boom");
        assert!(c.decode(Direction::Server, &encode(b'R', b"12")).is_err());
        assert!(c.decode(Direction::Server, &encode(b'D', &[0, 0, 0, 9, b'x'])).is_err());
    }

    #[test]
    fn filter_strips_padding_only_between_frames_under_any_chunking() {
        let mut whole = vec![0, 0];
        whole.extend(encode(b'D', &[0, 0, 0, 1, 0])); // a zero cell inside the payload
        whole.extend([0]);
        whole.extend(encode(b'R', &1u32.to_be_bytes()));
        let mut expect = encode(b'D', &[0, 0, 0, 1, 0]);
        expect.extend(encode(b'R', &1u32.to_be_bytes()));

        let mut c = opened();
        assert_eq!(c.filter(Direction::Server, &whole), expect);

        let mut c = opened();
        let mut bytewise = Vec::new();
        for b in &whole {
            bytewise.extend(c.filter(Direction::Server, &[*b]));
        }
        assert_eq!(bytewise, expect);
    }

    #[test]
    fn rewrite_masks_cells_by_column_and_recomputes_lengths() {
        let mut c = opened();
        c.enable_rewrite();
        let mut chunk = encode(b'C', b"id\0email");
        chunk.extend(row_frame(&[b"1", b"a@b"]));
        let mut seen = Vec::new();
        let out = c
            .rewrite(&chunk, &mut |col, val| {
                seen.push((col.to_string(), val.to_vec()));
                if col == "email" { b"***".to_vec() } else { val.to_vec() }
            })
            .unwrap();
        assert_eq!(seen, vec![("id".into(), b"1".to_vec()), ("email".into(), b"a@b".to_vec())]);
        let mut expect = encode(b'C', b"id\0email");
        expect.extend(row_frame(&[b"1", b"***"]));
        assert_eq!(out.bytes, expect);
        // Only the cell mask changed counts, and only a row with one.
        assert_eq!((out.cells, out.rows), (1, 1));
        let untouched = c.rewrite(&row_frame(&[b"3", b"e@f"]), &mut |_, v| v.to_vec()).unwrap();
        assert_eq!((untouched.cells, untouched.rows), (0, 0));

        // The codec holds a frame split across two chunks, then rebuilds it whole.
        let row = row_frame(&[b"2", b"c@d"]);
        let first = c.rewrite(&row[..7], &mut |_, v| v.to_vec()).unwrap();
        assert!(first.bytes.is_empty());
        let second = c.rewrite(&row[7..], &mut |_, _| b"x".to_vec()).unwrap();
        assert_eq!(second.bytes, row_frame(&[b"x", b"x"]));
        assert_eq!((second.cells, second.rows), (2, 1));
        assert!(c.flush(&mut |_, v| v.to_vec()).unwrap().bytes.is_empty());
    }

    #[test]
    fn deny_uses_the_configured_prefix() {
        let mut c = AcmeWire::default();
        c.open(&BTreeMap::from([("deny_prefix".to_string(), "NOPE".to_string())])).unwrap();
        assert_eq!(c.deny(Direction::Client, "blocked"), encode(b'E', b"NOPE: blocked"));
        assert!(c.open(&BTreeMap::from([("deny_prefix".to_string(), String::new())])).is_err());
    }

    #[test]
    fn credential_stays_off_the_statement_until_taken_once() {
        let mut c = opened();
        let auth = c.decode(Direction::Client, &encode(b'A', b"s3cret")).unwrap().statements.remove(0);
        assert_eq!(auth.metadata, BTreeMap::from([(VERB_KEY.into(), "AUTH".into()), (CREDENTIAL_KEY.into(), "1".into())]));
        let (token, scrubbed) = c.take_credential(auth.clone()).unwrap();
        assert_eq!(token, "s3cret");
        assert_eq!(scrubbed.metadata, BTreeMap::from([(VERB_KEY.into(), "AUTH".into())]));
        // The handle is spent: the same statement lifts nothing twice.
        assert!(c.take_credential(auth).is_none());
        assert!(c.take_credential(scrubbed).is_none());
        // Handles count up per connection, and another connection does not know them.
        let second = c.decode(Direction::Client, &encode(b'A', b"other")).unwrap().statements.remove(0);
        assert_eq!(second.metadata[CREDENTIAL_KEY], "2");
        assert!(opened().take_credential(second.clone()).is_none());
        assert_eq!(c.take_credential(second).unwrap().0, "other");
    }

    #[test]
    fn decode_refuses_to_hold_unbounded_credentials() {
        let mut c = opened();
        for _ in 0..MAX_HELD_CREDENTIALS {
            c.decode(Direction::Client, &encode(b'A', b"t")).unwrap();
        }
        assert!(c.decode(Direction::Client, &encode(b'A', b"t")).is_err());
    }

    #[test]
    fn content_renders_client_statements_only() {
        let c = opened();
        let mut stmt = Statement::new(Operation::Delete, "PURGE orders2024").with_metadata(VERB_KEY, "PURGE");
        let content = c.content(&stmt).unwrap();
        assert_eq!(content.text, "Protocol: x-acmewire\nVerb: PURGE\n\nPURGE orders2024");
        assert_eq!(content.cache_key, "PURGE|purge orders");
        stmt.direction = Some(Direction::Server);
        assert!(c.content(&stmt).is_none());
        assert!(c.content(&Statement::new(Operation::Other, "  ")).is_none());
    }
}
