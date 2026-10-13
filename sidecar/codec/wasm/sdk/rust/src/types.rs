//! The JSON vocabulary of ABI.md.
//!
//! Field names are the JSON names of `inspect.Statement`, which are
//! libhoop's, and the host refuses a payload with a name it does not know.
//! A codec builds these types: `cargo test` checks the names once here
//! instead of at every load.
//!
//! `Statement` carries no `protocol`: the host fills it and refuses a
//! guest that sets it, so a module cannot impersonate a built-in protocol.

use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};

/// The side of the connection that produced the bytes.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Direction {
    Client,
    Server,
}

impl Direction {
    /// Decodes the `dir` argument of an export: `0` is the client, `1` the
    /// server. Anything else is a host bug and traps, because guessing a
    /// direction would feed a request through the response path.
    pub fn from_abi(dir: u32) -> Direction {
        match dir {
            0 => Direction::Client,
            1 => Direction::Server,
            other => panic!("hoop-codec: direction {other} is neither 0 (client) nor 1 (server)"),
        }
    }
}

/// The normalized verb of a statement: the values `inspect.Operations()`
/// lists. The host refuses a statement with any other value, so the set is
/// closed here too.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Operation {
    Select,
    Insert,
    Update,
    Delete,
    Create,
    Drop,
    Alter,
    Truncate,
    Grant,
    Revoke,
    Call,
    Show,
    Set,
    Begin,
    Commit,
    Rollback,
    Get,
    Post,
    Put,
    Patch,
    Head,
    Options,
    Connect,
    Trace,
    ExecLine,
    EnvSet,
    SftpRead,
    SftpWrite,
    SftpRemove,
    SftpRename,
    SftpMkdir,
    SftpRmdir,
    SftpList,
    SftpStat,
    SftpSetstat,
    SftpSymlink,
    WsMessage,
    WsClose,
    /// Parsed, but not a verb the policy vocabulary classifies.
    Other,
    /// The statement could not be read. The default, because a rule
    /// naming `unknown` fails closed and a forgotten field must not pass
    /// as a read.
    #[default]
    Unknown,
}

/// Whether a statement reads a relation or changes it.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Access {
    Read,
    Write,
}

/// One relation a statement touches, and how. The guest lowercases names;
/// the policy compares them as written.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Relation {
    pub name: String,
    pub access: Access,
}

/// One field of a result set.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Column {
    pub name: String,
    /// The protocol's own type identifier; zero when it did not say.
    #[serde(default, skip_serializing_if = "is_zero")]
    pub data_type_oid: u32,
}

impl Column {
    pub fn named(name: impl Into<String>) -> Column {
        Column { name: name.into(), data_type_oid: 0 }
    }
}

/// The shape of a result set travelling back to the client, without the
/// values: a statement becomes an audit record, and a record holding the
/// rows it masked has un-masked them.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct ResultDetail {
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub columns: Vec<Column>,
    #[serde(default)]
    pub row_count: u64,
    #[serde(default, skip_serializing_if = "is_false")]
    pub truncated: bool,
}

/// One inspected unit of work: the document a policy evaluates.
///
/// A codec may leave `direction` as `None` on a decoded statement; the
/// host fills it with the direction of the `decode` call. A statement the
/// host hands back (`take_credential`, `content`) always carries it.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Statement {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub direction: Option<Direction>,
    pub text: String,
    pub operation: Operation,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub effects: Vec<Operation>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub relations: Vec<Relation>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub tables: Vec<String>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub database: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub result: Option<ResultDetail>,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub metadata: BTreeMap<String, String>,
}

impl Statement {
    /// A statement with the verb and text set and every other field empty.
    pub fn new(operation: Operation, text: impl Into<String>) -> Statement {
        Statement { operation, text: text.into(), ..Statement::default() }
    }

    /// Copies what the host lexer found onto the statement. An incomplete
    /// scan leaves `operation` unknown and records why under
    /// `sql.incomplete`, the key libhoop's own codecs use, so a policy
    /// reads the same metadata whichever codec produced the statement.
    pub fn with_analysis(mut self, analysis: SqlAnalysis) -> Statement {
        self.operation = analysis.operation;
        self.effects = analysis.effects;
        self.relations = analysis.relations;
        self.tables = analysis.tables;
        if !analysis.complete {
            self.operation = Operation::Unknown;
            self.metadata.insert("sql.incomplete".into(), analysis.reason);
        }
        self
    }

    /// Adds one metadata entry. Keys are `<protocol>.<name>`; a `metadata`
    /// rule matches them.
    pub fn with_metadata(mut self, key: impl Into<String>, value: impl Into<String>) -> Statement {
        self.metadata.insert(key.into(), value.into());
        self
    }

    pub fn with_result(mut self, result: ResultDetail) -> Statement {
        self.result = Some(result);
        self
    }
}

/// The host lexer's findings for one SQL statement; see `host::analyze_sql`.
#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct SqlAnalysis {
    pub operation: Operation,
    #[serde(default)]
    pub effects: Vec<Operation>,
    #[serde(default)]
    pub relations: Vec<Relation>,
    #[serde(default)]
    pub tables: Vec<String>,
    #[serde(default)]
    pub complete: bool,
    #[serde(default)]
    pub reason: String,
}

/// The result `Codec::decode` hands back for one chunk of input.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Decoded {
    pub statements: Vec<Statement>,
    /// Bytes of the input the codec finished with. Stop at an incomplete
    /// trailing message; the host passes the rest again, prefixed to the
    /// next read.
    pub consumed: usize,
}

impl Decoded {
    pub fn empty() -> Decoded {
        Decoded::default()
    }
}

/// The result `Codec::rewrite` and `Codec::flush` hand back.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Rewritten {
    /// The bytes to forward. May be empty while rows are held.
    pub bytes: Vec<u8>,
    /// The count of values `mask` returned changed. A value handed over
    /// and returned as-is is not masked, and the audit trail reports this.
    pub cells: u64,
    /// The count of rows with at least one changed value.
    pub rows: u64,
}

/// The text `Codec::content` renders for the AI analyzer.
#[derive(Clone, Debug, Default, PartialEq, Eq)]
pub struct Content {
    pub text: String,
    /// Names the statement's shape with literals stripped, so one verdict
    /// serves repeats. Empty means never cache.
    pub cache_key: String,
}

/// An optional export group, announced in the manifest's `capabilities`.
#[derive(Clone, Copy, Debug, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Capability {
    Deny,
    Filter,
    Rewrite,
    Credential,
    Content,
}

/// The host lexer dialect `analyze_sql` and `split_sql` run with.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum SqlDialect {
    Postgres,
    Mysql,
    Mssql,
    Clickhouse,
    Oracle,
    Googlesql,
}

/// The way the host maps connections onto instances.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Instances {
    PerConnection,
    PerLane,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum OptionType {
    String,
    Int,
    Bool,
    Enum,
}

/// One per-listener setting the control plane form renders and `open`
/// receives as a string.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct OptionSpec {
    pub name: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub label: Option<String>,
    #[serde(rename = "type")]
    pub kind: OptionType,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub values: Vec<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub default: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub help: Option<String>,
}

/// The manifest `describe` returns. `Manifest::new` sets `abi`; the rest is
/// the plug-in's to fill. `capabilities` must name the same set the
/// `export_codec!` list names, and `describe` traps when they differ,
/// because the host refuses a capability without its export and an export
/// without its capability.
#[derive(Clone, Debug, PartialEq, Eq, Serialize, Deserialize)]
pub struct Manifest {
    pub abi: u32,
    pub protocol: String,
    pub label: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub version: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub capabilities: Vec<Capability>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub sql_dialect: Option<SqlDialect>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub max_reassembly: Option<u64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub instances: Option<Instances>,
    #[serde(default, skip_serializing_if = "is_false")]
    pub wasi: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub call_timeout_ms: Option<u64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub memory_limit_pages: Option<u32>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub options: Vec<OptionSpec>,
}

/// The ABI version this SDK speaks.
pub const ABI_VERSION: u32 = 1;

impl Manifest {
    /// A manifest for `protocol` (which must start with `x-`) with the
    /// human `label`, at ABI version 1 and every optional key unset.
    pub fn new(protocol: impl Into<String>, label: impl Into<String>) -> Manifest {
        Manifest {
            abi: ABI_VERSION,
            protocol: protocol.into(),
            label: label.into(),
            version: String::new(),
            capabilities: Vec::new(),
            sql_dialect: None,
            max_reassembly: None,
            instances: None,
            wasi: false,
            call_timeout_ms: None,
            memory_limit_pages: None,
            options: Vec::new(),
        }
    }
}

// The wire forms below are private to the SDK: a codec returns the plain
// structs above and the export wrappers render these.

#[derive(Serialize)]
pub(crate) struct DecodeResult<'a> {
    pub statements: &'a [Statement],
    pub consumed: u64,
}

#[derive(Serialize)]
pub(crate) struct ErrorResult<'a> {
    pub error: &'a str,
}

#[derive(Serialize)]
pub(crate) struct RewriteResult {
    pub bytes: String,
    pub cells: u64,
    pub rows: u64,
}

#[derive(Serialize)]
pub(crate) struct CredentialResult<'a> {
    pub ok: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub credential: Option<&'a str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub statement: Option<&'a Statement>,
}

#[derive(Serialize)]
pub(crate) struct ContentResult<'a> {
    pub ok: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub text: Option<&'a str>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cache_key: Option<&'a str>,
}

fn is_zero(n: &u32) -> bool {
    *n == 0
}

fn is_false(b: &bool) -> bool {
    !*b
}
