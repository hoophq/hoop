//! x-resp: the Redis RESP2 wire protocol as a hoop Sidecar codec plug-in.
//!
//! A client sends one command per RESP array of bulk strings
//! (`*2\r\n$3\r\nGET\r\n$1\r\nk\r\n`) or per inline line (`PING\r\n`).
//! The server answers with a simple string (`+OK`), an error (`-ERR ...`),
//! an integer (`:3`), a bulk string (`$5\r\nhello\r\n`) or an array of
//! those, nested to any depth. Each command and each top-level reply is
//! one statement. `deny` renders a RESP error, `rewrite` masks every bulk
//! string value in a reply, `credential` lifts the password of AUTH and of
//! the AUTH clause of HELLO. The password stays in the codec; the
//! statement carries a handle, so no decode output holds the secret.

use std::collections::BTreeMap;

use hoop_codec::{
    Access, Capability, Codec, Decoded, Direction, Instances, Manifest, Operation, OptionSpec, OptionType, Relation,
    ResultDetail, Rewritten, Statement,
};

const PROTOCOL: &str = "x-resp";
const COMMAND_KEY: &str = "x-resp.command";
const KEY_KEY: &str = "x-resp.key";
const CREDENTIAL_KEY: &str = "x-resp.credential";
const REPLY_KEY: &str = "x-resp.reply";
const DEFAULT_MAX_TEXT_BYTES: usize = 4096;
/// Redis refuses an inline line past 64 KiB. The codec gives up waiting
/// for its newline there instead of buffering to `max_reassembly`, and
/// refuses a line that arrived whole past the same bound. An option,
/// so a fixture can prove both paths without a 64 KiB script.
const DEFAULT_MAX_INLINE_BYTES: usize = 64 * 1024;
/// Redis refuses a multibulk count past 1024*1024.
const MAX_ARRAY_LEN: i64 = 1024 * 1024;
/// Redis's default `proto-max-bulk-len`.
const MAX_BULK_LEN: i64 = 512 * 1024 * 1024;
/// Arrays nest recursively on the wasm stack; Redis replies never go
/// deeper than a few levels.
const MAX_DEPTH: usize = 32;

pub fn manifest() -> Manifest {
    let mut m = Manifest::new(PROTOCOL, "Redis RESP");
    m.version = env!("CARGO_PKG_VERSION").into();
    m.capabilities = vec![Capability::Deny, Capability::Rewrite, Capability::Credential];
    m.instances = Some(Instances::PerConnection);
    m.options = vec![
        OptionSpec {
            name: "max_text_bytes".into(),
            label: Some("Max text bytes".into()),
            kind: OptionType::Int,
            values: Vec::new(),
            default: Some(DEFAULT_MAX_TEXT_BYTES.to_string()),
            help: Some("Bytes of a command line kept as the statement text; longer lines are cut.".into()),
        },
        OptionSpec {
            name: "max_inline_bytes".into(),
            label: Some("Max inline bytes".into()),
            kind: OptionType::Int,
            values: Vec::new(),
            default: Some(DEFAULT_MAX_INLINE_BYTES.to_string()),
            help: Some("Longest inline command line before the LF, CR included; a longer one drops the connection as malformed. Redis's own bound is 65536.".into()),
        },
    ];
    m
}

hoop_codec::export_codec!(Resp, manifest, [deny, rewrite, credential]);

/// One connection. The rewrite path keeps its own buffer because it
/// frames the server stream a second time, on the chunks the host
/// forwards after `decode` saw them. Credentials wait here, keyed by the
/// handle the statement carries, until `take_credential` asks for them.
#[derive(Default)]
pub struct Resp {
    max_text_bytes: usize,
    max_inline_bytes: usize,
    rewrite_buf: Vec<u8>,
    next_handle: u64,
    secrets: BTreeMap<u64, String>,
}

/// Credentials outstanding per connection. The host takes each one on
/// the call after `decode`; a lane without per-request identity never
/// asks, and a client that re-authenticates in a loop must not grow the
/// instance, so the oldest entry goes when the map is full.
const MAX_SECRETS: usize = 16;

/// One RESP2 value at the front of a buffer; the slices borrow it.
#[derive(Debug, PartialEq, Eq)]
enum Value<'a> {
    Simple(&'a [u8]),
    Error(&'a [u8]),
    /// The digits as sent, so re-encoding reproduces the wire bytes.
    Integer(&'a [u8]),
    /// `None` is the null bulk string, `$-1`.
    Bulk(Option<&'a [u8]>),
    /// `None` is the null array, `*-1`.
    Array(Option<Vec<Value<'a>>>),
}

/// `Ok(None)` means the input ends inside the item: the host buffers and
/// calls again. `Ok(Some((item, size)))` includes the CRLF in `size`.
type Parsed<T> = Result<Option<(T, usize)>, String>;

/// A line up to CRLF, for simple strings and errors. A bare LF or CR is
/// outside RESP.
fn line(data: &[u8]) -> Parsed<&[u8]> {
    let mut i = 0;
    while let Some(&b) = data.get(i) {
        match b {
            b'\n' => return Err("x-resp: bare LF in a line".into()),
            b'\r' => {
                return match data.get(i + 1) {
                    None => Ok(None),
                    Some(b'\n') => Ok(Some((&data[..i], i + 2))),
                    Some(_) => Err("x-resp: CR not followed by LF".into()),
                };
            }
            _ => i += 1,
        }
    }
    Ok(None)
}

/// A signed decimal line, as `$`, `*` and `:` carry it. Each byte is
/// checked as it arrives, so `*x` fails on its second byte instead of
/// after the host buffered to `max_reassembly` waiting for a CRLF.
fn number(data: &[u8]) -> Parsed<i64> {
    let start = usize::from(data.first() == Some(&b'-'));
    let mut i = start;
    let mut n: i64 = 0;
    loop {
        let Some(&b) = data.get(i) else { return Ok(None) };
        match b {
            b'0'..=b'9' if i - start < 18 => {
                n = n * 10 + i64::from(b - b'0');
                i += 1;
            }
            b'\r' if i > start => {
                return match data.get(i + 1) {
                    None => Ok(None),
                    Some(b'\n') => Ok(Some((if start == 1 { -n } else { n }, i + 2))),
                    Some(_) => Err("x-resp: CR not followed by LF".into()),
                };
            }
            _ => return Err(format!("x-resp: byte 0x{b:02x} in a number")),
        }
    }
}

/// One value, including the type byte.
fn value(data: &[u8], depth: usize) -> Parsed<Value<'_>> {
    let Some(&kind) = data.first() else { return Ok(None) };
    let body = &data[1..];
    match kind {
        b'+' => Ok(line(body)?.map(|(s, n)| (Value::Simple(s), n + 1))),
        b'-' => Ok(line(body)?.map(|(s, n)| (Value::Error(s), n + 1))),
        b':' => Ok(number(body)?.map(|(_, n)| (Value::Integer(&body[..n - 2]), n + 1))),
        b'$' => bulk(body),
        b'*' => array(body, depth),
        other => Err(format!("x-resp: byte 0x{other:02x} does not start a RESP value")),
    }
}

/// The body of a bulk string after `$`: a length line, the payload, CRLF.
fn bulk(body: &[u8]) -> Parsed<Value<'_>> {
    let Some((len, n)) = number(body)? else { return Ok(None) };
    if len == -1 {
        return Ok(Some((Value::Bulk(None), n + 1)));
    }
    if !(0..=MAX_BULK_LEN).contains(&len) {
        return Err(format!("x-resp: bulk string length {len} is out of range"));
    }
    let end = n + len as usize;
    match (body.get(end), body.get(end + 1)) {
        (Some(b'\r'), Some(b'\n')) => Ok(Some((Value::Bulk(Some(&body[n..end])), end + 3))),
        (Some(b'\r'), None) | (None, _) => Ok(None),
        _ => Err("x-resp: bulk string payload is not followed by CRLF".into()),
    }
}

/// The body of an array after `*`: a count line, then that many values.
fn array(body: &[u8], depth: usize) -> Parsed<Value<'_>> {
    let Some((len, n)) = number(body)? else { return Ok(None) };
    if len == -1 {
        return Ok(Some((Value::Array(None), n + 1)));
    }
    if !(0..=MAX_ARRAY_LEN).contains(&len) {
        return Err(format!("x-resp: array length {len} is out of range"));
    }
    if depth >= MAX_DEPTH {
        return Err(format!("x-resp: arrays nest past {MAX_DEPTH} levels"));
    }
    let mut items = Vec::with_capacity((len as usize).min(64));
    let mut pos = n;
    for _ in 0..len {
        let Some((item, size)) = value(&body[pos..], depth + 1)? else { return Ok(None) };
        items.push(item);
        pos += size;
    }
    Ok(Some((Value::Array(Some(items)), pos + 1)))
}

/// The RESP encoding of `value`, with every bulk string payload passed
/// through `mask`. Returns the count of payloads `mask` changed, which
/// is what the audit trail reports as masked.
fn encode_masked(value: &Value<'_>, out: &mut Vec<u8>, mask: &mut dyn FnMut(&str, &[u8]) -> Vec<u8>) -> u64 {
    match value {
        Value::Simple(s) => {
            out.push(b'+');
            out.extend_from_slice(s);
            out.extend_from_slice(b"\r\n");
            0
        }
        Value::Error(s) => {
            out.push(b'-');
            out.extend_from_slice(s);
            out.extend_from_slice(b"\r\n");
            0
        }
        Value::Integer(digits) => {
            out.push(b':');
            out.extend_from_slice(digits);
            out.extend_from_slice(b"\r\n");
            0
        }
        Value::Bulk(None) => {
            out.extend_from_slice(b"$-1\r\n");
            0
        }
        Value::Bulk(Some(payload)) => {
            let masked = mask("", payload);
            let changed = u64::from(masked.as_slice() != *payload);
            out.extend_from_slice(format!("${}\r\n", masked.len()).as_bytes());
            out.extend_from_slice(&masked);
            out.extend_from_slice(b"\r\n");
            changed
        }
        Value::Array(None) => {
            out.extend_from_slice(b"*-1\r\n");
            0
        }
        Value::Array(Some(items)) => {
            out.extend_from_slice(format!("*{}\r\n", items.len()).as_bytes());
            items.iter().map(|item| encode_masked(item, out, mask)).sum()
        }
    }
}

/// The argument positions a command reads as keys.
#[derive(Clone, Copy)]
enum Keys {
    None,
    First,
    All,
    /// MSET: key value key value ...
    EveryOther,
    /// EVAL: script numkeys key ... arg ...
    Eval,
}

/// The policy verb and key positions of a command. Anything else is
/// `other` with no keys: the codec does not know its arity.
fn classify(command: &str) -> (Operation, Keys) {
    match command {
        "GET" | "TTL" | "STRLEN" | "HGET" | "HGETALL" | "LRANGE" | "SMEMBERS" | "ZRANGE" | "TYPE" => {
            (Operation::Select, Keys::First)
        }
        "MGET" | "EXISTS" => (Operation::Select, Keys::All),
        "SCAN" => (Operation::Select, Keys::None),
        "SET" | "INCR" | "DECR" | "APPEND" | "HSET" | "LPUSH" | "RPUSH" | "SADD" | "ZADD" | "EXPIRE" | "PERSIST" => {
            (Operation::Update, Keys::First)
        }
        "MSET" => (Operation::Update, Keys::EveryOther),
        "DEL" | "UNLINK" => (Operation::Delete, Keys::All),
        "HDEL" | "LPOP" | "RPOP" | "SREM" | "ZREM" => (Operation::Delete, Keys::First),
        "FLUSHALL" | "FLUSHDB" => (Operation::Truncate, Keys::None),
        "KEYS" | "INFO" | "DBSIZE" | "CLIENT" | "COMMAND" => (Operation::Show, Keys::None),
        "CONFIG" | "SELECT" | "AUTH" => (Operation::Set, Keys::None),
        "MULTI" => (Operation::Begin, Keys::None),
        "EXEC" => (Operation::Commit, Keys::None),
        "DISCARD" => (Operation::Rollback, Keys::None),
        "EVAL" | "EVALSHA" => (Operation::Call, Keys::Eval),
        _ => (Operation::Other, Keys::None),
    }
}

/// The key arguments of `args`, lowercased and without repeats.
fn keys(which: Keys, args: &[&[u8]]) -> Vec<String> {
    let rest = args.get(1..).unwrap_or(&[]);
    let picked: Vec<&[u8]> = match which {
        Keys::None => Vec::new(),
        Keys::First => rest.iter().take(1).copied().collect(),
        Keys::All => rest.to_vec(),
        Keys::EveryOther => rest.iter().step_by(2).copied().collect(),
        Keys::Eval => {
            let count = rest.get(1).and_then(|n| text(n).parse::<usize>().ok()).unwrap_or(0);
            rest.iter().skip(2).take(count).copied().collect()
        }
    };
    let mut keys: Vec<String> = Vec::with_capacity(picked.len());
    for key in picked {
        let key = text(key).to_lowercase();
        if !keys.contains(&key) {
            keys.push(key);
        }
    }
    keys
}

fn text(bytes: &[u8]) -> String {
    String::from_utf8_lossy(bytes).into_owned()
}

/// The credential of an AUTH command, or of the AUTH clause of HELLO, and
/// the argument positions that carry it. `user:password` when both are
/// present, the password alone otherwise.
fn credential(command: &str, args: &[&[u8]]) -> Option<(String, std::ops::Range<usize>)> {
    let at = match command {
        "AUTH" => 1,
        "HELLO" => args.iter().position(|a| a.eq_ignore_ascii_case(b"AUTH"))? + 1,
        _ => return None,
    };
    let parts: Vec<String> = args.get(at..)?.iter().take(2).map(|a| text(a)).collect();
    if parts.is_empty() {
        return None;
    }
    let end = if command == "AUTH" { at + parts.len() } else { at + 2 };
    if end > args.len() {
        return None;
    }
    Some((parts.join(":"), at..end))
}

impl Resp {
    fn truncate(&self, mut s: String) -> String {
        if s.len() > self.max_text_bytes {
            let mut cut = self.max_text_bytes;
            while !s.is_char_boundary(cut) {
                cut -= 1;
            }
            s.truncate(cut);
        }
        s
    }

    /// The statement of one client command. The credential never reaches
    /// the text or the metadata: the statement carries a handle into
    /// `secrets`, and `take_credential` resolves it.
    fn command_statement(&mut self, args: &[&[u8]]) -> Statement {
        let command = text(args[0]).to_ascii_uppercase();
        let (operation, which) = classify(&command);
        let secret = credential(&command, args);
        let hidden = secret.as_ref().map(|(_, range)| range.clone()).unwrap_or(0..0);
        let words: Vec<String> =
            args.iter().enumerate().filter(|(i, _)| !hidden.contains(i)).map(|(_, a)| text(a)).collect();
        let mut stmt = Statement::new(operation, self.truncate(words.join(" "))).with_metadata(COMMAND_KEY, &command);
        if operation != Operation::Other {
            stmt.effects = vec![operation];
        }
        let tables = keys(which, args);
        if let Some(first) = tables.first() {
            stmt = stmt.with_metadata(KEY_KEY, first);
        }
        let access = if operation == Operation::Select { Access::Read } else { Access::Write };
        stmt.relations = tables.iter().map(|name| Relation { name: name.clone(), access }).collect();
        stmt.tables = tables;
        if let Some((value, _)) = secret {
            stmt = stmt.with_metadata(CREDENTIAL_KEY, self.hold(value).to_string());
        }
        stmt
    }

    /// Stores a credential and returns its handle.
    fn hold(&mut self, value: String) -> u64 {
        if self.secrets.len() >= MAX_SECRETS {
            let oldest = *self.secrets.keys().next().expect("a full map has a first key");
            self.secrets.remove(&oldest);
        }
        self.next_handle += 1;
        self.secrets.insert(self.next_handle, value);
        self.next_handle
    }

    /// One command at the front of `data`: a RESP array of bulk strings,
    /// or an inline line. `None` inside the pair is an empty command,
    /// which Redis ignores.
    fn command(&mut self, data: &[u8]) -> Parsed<Option<Statement>> {
        let first = data[0];
        if first == b'*' {
            let Some((parsed, size)) = value(data, 0)? else { return Ok(None) };
            let Value::Array(Some(items)) = parsed else {
                return Err("x-resp: a command is an array of bulk strings".into());
            };
            let mut args = Vec::with_capacity(items.len());
            for item in &items {
                let Value::Bulk(Some(arg)) = item else {
                    return Err("x-resp: a command argument is not a bulk string".into());
                };
                args.push(*arg);
            }
            let stmt = if args.is_empty() { None } else { Some(self.command_statement(&args)) };
            return Ok(Some((stmt, size)));
        }
        if !first.is_ascii_graphic() && !first.is_ascii_whitespace() {
            return Err(format!("x-resp: byte 0x{first:02x} does not start a command"));
        }
        let Some(end) = data.iter().position(|b| *b == b'\n') else {
            if data.len() >= self.max_inline_bytes {
                return Err(format!("x-resp: inline command exceeds {} bytes", self.max_inline_bytes));
            }
            return Ok(None);
        };
        // The same bound on a line that arrived whole, so a replay in one
        // read fails where a byte-at-a-time replay fails.
        if end >= self.max_inline_bytes {
            return Err(format!("x-resp: inline command exceeds {} bytes", self.max_inline_bytes));
        }
        let args: Vec<&[u8]> = data[..end].split(u8::is_ascii_whitespace).filter(|a| !a.is_empty()).collect();
        let stmt = if args.is_empty() { None } else { Some(self.command_statement(&args)) };
        Ok(Some((stmt, end + 1)))
    }

    /// One top-level reply at the front of `data`. The text names the
    /// type and, for a bulk string or an array, the size; values stay
    /// out of the audit trail.
    fn reply(&self, data: &[u8]) -> Parsed<Statement> {
        let Some((parsed, size)) = value(data, 0)? else { return Ok(None) };
        let (text, kind, rows) = match &parsed {
            Value::Simple(s) => (format!("+{}", text(s)), "simple", None),
            Value::Error(s) => (format!("-{}", text(s)), "error", None),
            Value::Integer(digits) => (format!(":{}", text(digits)), "integer", None),
            Value::Bulk(None) => ("$-1".to_string(), "bulk", None),
            Value::Bulk(Some(payload)) => (format!("${}", payload.len()), "bulk", None),
            Value::Array(None) => ("*-1".to_string(), "array", Some(0)),
            Value::Array(Some(items)) => (format!("*{}", items.len()), "array", Some(items.len() as u64)),
        };
        let mut stmt = Statement::new(Operation::Other, self.truncate(text)).with_metadata(REPLY_KEY, kind);
        if let Some(row_count) = rows {
            stmt = stmt.with_result(ResultDetail { row_count, ..ResultDetail::default() });
        }
        Ok(Some((stmt, size)))
    }
}

/// One integer option, which must be positive when set.
fn positive_int(options: &BTreeMap<String, String>, name: &str, default: usize) -> Result<usize, String> {
    match options.get(name) {
        None => Ok(default),
        Some(raw) => match raw.trim().parse::<usize>() {
            Ok(n) if n > 0 => Ok(n),
            _ => Err(format!("x-resp: {name} {raw:?} is not a positive integer")),
        },
    }
}

impl Codec for Resp {
    fn open(&mut self, options: &BTreeMap<String, String>) -> Result<(), String> {
        self.max_text_bytes = positive_int(options, "max_text_bytes", DEFAULT_MAX_TEXT_BYTES)?;
        self.max_inline_bytes = positive_int(options, "max_inline_bytes", DEFAULT_MAX_INLINE_BYTES)?;
        Ok(())
    }

    fn decode(&mut self, dir: Direction, data: &[u8]) -> Result<Decoded, String> {
        let mut out = Decoded::empty();
        while out.consumed < data.len() {
            let rest = &data[out.consumed..];
            let size = match dir {
                Direction::Client => {
                    let Some((stmt, size)) = self.command(rest)? else { break };
                    out.statements.extend(stmt);
                    size
                }
                Direction::Server => {
                    let Some((stmt, size)) = self.reply(rest)? else { break };
                    out.statements.push(stmt);
                    size
                }
            };
            out.consumed += size;
        }
        Ok(out)
    }

    /// A RESP error. A line break inside the message would end the frame
    /// early and leave the rest as a second reply.
    fn deny(&self, _dir: Direction, message: &str) -> Vec<u8> {
        let flat: String = message.chars().map(|c| if c == '\r' || c == '\n' { ' ' } else { c }).collect();
        format!("-ERR {flat}\r\n").into_bytes()
    }

    fn enable_rewrite(&mut self) {}

    /// Rebuilds every complete reply with masked bulk strings. A reply
    /// split across chunks waits in `rewrite_buf` for its tail. One row
    /// is a top-level bulk string or one element of a top-level array.
    fn rewrite(&mut self, data: &[u8], mask: &mut dyn FnMut(&str, &[u8]) -> Vec<u8>) -> Result<Rewritten, String> {
        self.rewrite_buf.extend_from_slice(data);
        let mut out = Rewritten::default();
        let mut consumed = 0;
        while let Some((parsed, size)) = value(&self.rewrite_buf[consumed..], 0)? {
            match &parsed {
                Value::Array(Some(items)) => {
                    out.bytes.extend_from_slice(format!("*{}\r\n", items.len()).as_bytes());
                    for item in items {
                        let changed = encode_masked(item, &mut out.bytes, mask);
                        out.cells += changed;
                        out.rows += u64::from(changed > 0);
                    }
                }
                _ => {
                    let changed = encode_masked(&parsed, &mut out.bytes, mask);
                    out.cells += changed;
                    out.rows += u64::from(changed > 0);
                }
            }
            consumed += size;
        }
        self.rewrite_buf.drain(..consumed);
        Ok(out)
    }

    /// The buffer holds at most one incomplete reply, whose values `mask`
    /// never saw. The connection is ending, so `flush` drops it.
    fn flush(&mut self, _mask: &mut dyn FnMut(&str, &[u8]) -> Vec<u8>) -> Result<Rewritten, String> {
        self.rewrite_buf.clear();
        Ok(Rewritten::default())
    }

    /// Resolves the statement's handle, forgets the entry and scrubs the
    /// key. A handle already taken, or one this connection never issued,
    /// is no credential.
    fn take_credential(&mut self, mut stmt: Statement) -> Option<(String, Statement)> {
        let handle = stmt.metadata.remove(CREDENTIAL_KEY)?.parse::<u64>().ok()?;
        let secret = self.secrets.remove(&handle)?;
        Some((secret, stmt))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn opened() -> Resp {
        let mut c = Resp::default();
        c.open(&BTreeMap::new()).unwrap();
        c
    }

    fn cmd(args: &[&str]) -> Vec<u8> {
        let mut out = format!("*{}\r\n", args.len()).into_bytes();
        for a in args {
            out.extend_from_slice(format!("${}\r\n{a}\r\n", a.len()).as_bytes());
        }
        out
    }

    fn decode_one(c: &mut Resp, dir: Direction, bytes: &[u8]) -> Statement {
        let d = c.decode(dir, bytes).unwrap();
        assert_eq!((d.consumed, d.statements.len()), (bytes.len(), 1));
        d.statements.into_iter().next().unwrap()
    }

    #[test]
    fn commands_map_to_operations_and_keys() {
        let mut c = opened();
        let s = decode_one(&mut c, Direction::Client, &cmd(&["set", "User:1", "bob"]));
        assert_eq!((s.operation, s.text.as_str()), (Operation::Update, "set User:1 bob"));
        assert_eq!(s.tables, vec!["user:1"]);
        assert_eq!(s.relations, vec![Relation { name: "user:1".into(), access: Access::Write }]);
        assert_eq!(s.metadata[COMMAND_KEY], "SET");
        assert_eq!(s.metadata[KEY_KEY], "user:1");

        let s = decode_one(&mut c, Direction::Client, &cmd(&["MGET", "a", "B", "a"]));
        assert_eq!(s.operation, Operation::Select);
        assert_eq!(s.tables, vec!["a", "b"]);
        assert_eq!(s.relations[0].access, Access::Read);

        let s = decode_one(&mut c, Direction::Client, &cmd(&["MSET", "k1", "v1", "k2", "v2"]));
        assert_eq!(s.tables, vec!["k1", "k2"]);

        let s = decode_one(&mut c, Direction::Client, &cmd(&["EVAL", "return 1", "2", "x", "y", "arg"]));
        assert_eq!((s.operation, s.tables), (Operation::Call, vec!["x".into(), "y".into()]));

        let s = decode_one(&mut c, Direction::Client, &cmd(&["FLUSHALL"]));
        assert_eq!((s.operation, s.tables.len()), (Operation::Truncate, 0));
        assert!(!s.metadata.contains_key(KEY_KEY));

        let s = decode_one(&mut c, Direction::Client, &cmd(&["OBJECT", "ENCODING", "k"]));
        assert_eq!((s.operation, s.effects.len(), s.tables.len()), (Operation::Other, 0, 0));
    }

    #[test]
    fn inline_commands_and_empty_arrays() {
        let mut c = opened();
        let s = decode_one(&mut c, Direction::Client, b"PING\r\n");
        assert_eq!((s.operation, s.text.as_str()), (Operation::Other, "PING"));
        let s = decode_one(&mut c, Direction::Client, b"  get   k1 \n");
        assert_eq!((s.operation, s.text.as_str()), (Operation::Select, "get k1"));
        let d = c.decode(Direction::Client, b"*0\r\n\r\n").unwrap();
        assert_eq!((d.consumed, d.statements.len()), (6, 0));
    }

    #[test]
    fn partial_input_stops_at_the_command_start() {
        let mut c = opened();
        let whole = cmd(&["GET", "hello"]);
        for cut in 0..whole.len() {
            let d = c.decode(Direction::Client, &whole[..cut]).unwrap();
            assert_eq!((d.consumed, d.statements.len()), (0, 0), "cut at {cut}");
        }
        let mut two = cmd(&["PING"]);
        two.extend_from_slice(&whole[..whole.len() - 1]);
        let d = c.decode(Direction::Client, &two).unwrap();
        assert_eq!((d.consumed, d.statements.len()), (cmd(&["PING"]).len(), 1));
        let d = c.decode(Direction::Client, b"PI").unwrap();
        assert_eq!((d.consumed, d.statements.len()), (0, 0));
    }

    #[test]
    fn malformed_input_fails_at_the_first_bad_byte() {
        let mut c = opened();
        assert!(c.decode(Direction::Client, b"*x").is_err());
        assert!(c.decode(Direction::Client, b"\x00").is_err());
        assert!(c.decode(Direction::Client, b"*1\r\n+PING\r\n").is_err(), "a command argument must be bulk");
        assert!(c.decode(Direction::Client, b"*1\r\n$2\r\nabc\r\n").is_err(), "payload longer than its length");
        assert!(c.decode(Direction::Client, b"*1\r\n$-5\r\n").is_err());
        assert!(c.decode(Direction::Server, b"\xff").is_err());
        assert!(c.decode(Direction::Server, b"+OK\n").is_err(), "a bare LF");
        let long = vec![b'A'; DEFAULT_MAX_INLINE_BYTES];
        assert!(c.decode(Direction::Client, &long).is_err(), "no newline at the bound");
    }

    #[test]
    fn inline_bound_applies_to_a_complete_line() {
        let mut c = Resp::default();
        c.open(&BTreeMap::from([("max_inline_bytes".to_string(), "16".to_string())])).unwrap();
        assert!(c.decode(Direction::Client, b"AAAAAAAAAAAAAAAA").is_err(), "16 bytes without a newline");
        assert!(c.decode(Direction::Client, b"AAAAAAAAAAAAAAA\r\n").is_err(), "a complete line of 16 bytes before the LF");
        assert!(c.decode(Direction::Client, b"AAAAAAAAAAAAAAAAAAAA\r\nPING\r\n").is_err(), "a longer complete line, whatever follows");
        // The bound counts every byte before the LF, the CR included.
        let s = decode_one(&mut c, Direction::Client, b"AAAAAAAAAAAAAA\r\n");
        assert_eq!(s.text, "AAAAAAAAAAAAAA");
        assert_eq!(c.decode(Direction::Client, b"AAAAAAAAAAAAAAA").unwrap().consumed, 0, "15 bytes still wait for the LF");
        assert!(Resp::default().open(&BTreeMap::from([("max_inline_bytes".to_string(), "0".to_string())])).is_err());
    }

    #[test]
    fn long_text_is_cut_on_a_char_boundary() {
        let mut c = Resp::default();
        c.open(&BTreeMap::from([("max_text_bytes".to_string(), "6".to_string())])).unwrap();
        let s = decode_one(&mut c, Direction::Client, &cmd(&["SET", "k", "ééé"]));
        assert_eq!(s.text, "SET k ");
        assert!(Resp::default().open(&BTreeMap::from([("max_text_bytes".to_string(), "0".to_string())])).is_err());
        assert!(Resp::default().open(&BTreeMap::from([("max_text_bytes".to_string(), "many".to_string())])).is_err());
    }

    #[test]
    fn replies_render_short_and_consume_nested_arrays() {
        let mut c = opened();
        let bytes = b"+OK\r\n-ERR unknown command\r\n:3\r\n$5\r\nhello\r\n$-1\r\n*2\r\n*2\r\n$1\r\na\r\n:1\r\n$2\r\nbb\r\n*-1\r\n";
        let d = c.decode(Direction::Server, bytes).unwrap();
        assert_eq!(d.consumed, bytes.len());
        let got: Vec<(&str, &str)> =
            d.statements.iter().map(|s| (s.text.as_str(), s.metadata[REPLY_KEY].as_str())).collect();
        assert_eq!(
            got,
            vec![
                ("+OK", "simple"),
                ("-ERR unknown command", "error"),
                (":3", "integer"),
                ("$5", "bulk"),
                ("$-1", "bulk"),
                ("*2", "array"),
                ("*-1", "array"),
            ]
        );
        assert_eq!(d.statements[5].result.as_ref().unwrap().row_count, 2);
        assert!(d.statements.iter().all(|s| s.operation == Operation::Other));

        let d = c.decode(Direction::Server, b"*2\r\n$1\r\na\r\n$1\r\n").unwrap();
        assert_eq!((d.consumed, d.statements.len()), (0, 0));
    }

    #[test]
    fn rewrite_masks_bulk_values_inside_arrays_and_recomputes_lengths() {
        let mut c = opened();
        c.enable_rewrite();
        let reply = b"*3\r\n$3\r\nbob\r\n*2\r\n$5\r\na@b.c\r\n:7\r\n$-1\r\n+OK\r\n";
        let mut seen = Vec::new();
        let out = c
            .rewrite(reply, &mut |col, val| {
                seen.push((col.to_string(), val.to_vec()));
                if val.contains(&b'@') { b"***".to_vec() } else { val.to_vec() }
            })
            .unwrap();
        assert_eq!(seen, vec![("".to_string(), b"bob".to_vec()), ("".to_string(), b"a@b.c".to_vec())]);
        assert_eq!(out.bytes, b"*3\r\n$3\r\nbob\r\n*2\r\n$3\r\n***\r\n:7\r\n$-1\r\n+OK\r\n");
        assert_eq!((out.cells, out.rows), (1, 1));

        let whole = b"$4\r\nsecr\r\n";
        let first = c.rewrite(&whole[..6], &mut |_, v| v.to_vec()).unwrap();
        assert!(first.bytes.is_empty());
        let second = c.rewrite(&whole[6..], &mut |_, _| b"x".to_vec()).unwrap();
        assert_eq!(second.bytes, b"$1\r\nx\r\n");
        assert_eq!((second.cells, second.rows), (1, 1));
        assert!(c.rewrite(b"$2\r\n", &mut |_, v| v.to_vec()).unwrap().bytes.is_empty());
        assert!(c.flush(&mut |_, v| v.to_vec()).unwrap().bytes.is_empty());
        assert!(c.rewrite(b"+OK\r\n", &mut |_, v| v.to_vec()).unwrap().bytes == b"+OK\r\n");
    }

    #[test]
    fn deny_is_a_resp_error_on_one_line() {
        let c = opened();
        assert_eq!(c.deny(Direction::Client, "blocked\r\nby policy"), b"-ERR blocked  by policy\r\n");
    }

    #[test]
    fn credential_is_held_behind_a_handle_until_taken() {
        let mut c = opened();
        let auth = decode_one(&mut c, Direction::Client, &cmd(&["AUTH", "s3cret"]));
        assert_eq!((auth.operation, auth.text.as_str()), (Operation::Set, "AUTH"));
        assert_eq!(auth.metadata[CREDENTIAL_KEY], "1");
        assert!(!format!("{auth:?}").contains("s3cret"), "the secret is in decode output");
        let (secret, scrubbed) = c.take_credential(auth.clone()).unwrap();
        assert_eq!(secret, "s3cret");
        assert!(!scrubbed.metadata.contains_key(CREDENTIAL_KEY));
        assert_eq!(scrubbed.metadata[COMMAND_KEY], "AUTH");
        assert!(c.take_credential(auth).is_none(), "a handle resolves once");

        let auth = decode_one(&mut c, Direction::Client, &cmd(&["auth", "alice", "pw"]));
        assert_eq!((auth.text.as_str(), auth.metadata[CREDENTIAL_KEY].as_str()), ("auth", "2"));
        assert_eq!(c.take_credential(auth).unwrap().0, "alice:pw");

        let hello = decode_one(&mut c, Direction::Client, &cmd(&["HELLO", "2", "AUTH", "alice", "pw", "SETNAME", "cli"]));
        assert_eq!(hello.text, "HELLO 2 AUTH SETNAME cli");
        assert_eq!(c.take_credential(hello).unwrap().0, "alice:pw");

        let bare = decode_one(&mut c, Direction::Client, &cmd(&["AUTH"]));
        assert!(c.take_credential(bare).is_none());

        // A client that never has its credential taken does not grow the codec.
        for i in 0..(MAX_SECRETS + 4) {
            decode_one(&mut c, Direction::Client, &cmd(&["AUTH", &format!("pw{i}")]));
        }
        assert_eq!(c.secrets.len(), MAX_SECRETS);
        let last = decode_one(&mut c, Direction::Client, &cmd(&["AUTH", "newest"]));
        assert_eq!(c.take_credential(last).unwrap().0, "newest");
    }
}
