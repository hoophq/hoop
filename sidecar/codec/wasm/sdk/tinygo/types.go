// Package codec is the Go SDK for hoop codec plug-ins (../../abi/ABI.md),
// compiled with TinyGo for wasm32-unknown-unknown.
//
// A plug-in implements Codec, and the capability interfaces it supports,
// then calls Serve from an init function. The package carries the
// required exports; each optional export is in a file behind a build tag
// (hoop_deny, hoop_filter, hoop_rewrite, hoop_credential, hoop_content),
// so the exports of the module match the capabilities of the manifest by
// construction. See README.md for the build line.
package codec

// Direction says which side of the connection produced the bytes.
type Direction string

const (
	Client Direction = "client"
	Server Direction = "server"
)

// directionOf decodes the dir argument of an export. Anything but 0 and 1
// is a host bug and traps, because guessing feeds a request through the
// response path.
func directionOf(dir uint32) Direction {
	switch dir {
	case 0:
		return Client
	case 1:
		return Server
	}
	panic("hoop-codec: direction is neither 0 (client) nor 1 (server)")
}

// Operation is the normalized verb of a statement: one of the values
// inspect.Operations() lists. The host refuses any other value.
type Operation string

const (
	OpSelect   Operation = "select"
	OpInsert   Operation = "insert"
	OpUpdate   Operation = "update"
	OpDelete   Operation = "delete"
	OpCreate   Operation = "create"
	OpDrop     Operation = "drop"
	OpAlter    Operation = "alter"
	OpTruncate Operation = "truncate"
	OpGrant    Operation = "grant"
	OpRevoke   Operation = "revoke"
	OpCall     Operation = "call"
	OpShow     Operation = "show"
	OpSet      Operation = "set"
	OpBegin    Operation = "begin"
	OpCommit   Operation = "commit"
	OpRollback Operation = "rollback"

	OpGet     Operation = "get"
	OpPost    Operation = "post"
	OpPut     Operation = "put"
	OpPatch   Operation = "patch"
	OpHead    Operation = "head"
	OpOptions Operation = "options"
	OpConnect Operation = "connect"
	OpTrace   Operation = "trace"

	OpExecLine Operation = "exec_line"
	OpEnvSet   Operation = "env_set"

	OpSFTPRead    Operation = "sftp_read"
	OpSFTPWrite   Operation = "sftp_write"
	OpSFTPRemove  Operation = "sftp_remove"
	OpSFTPRename  Operation = "sftp_rename"
	OpSFTPMkdir   Operation = "sftp_mkdir"
	OpSFTPRmdir   Operation = "sftp_rmdir"
	OpSFTPList    Operation = "sftp_list"
	OpSFTPStat    Operation = "sftp_stat"
	OpSFTPSetstat Operation = "sftp_setstat"
	OpSFTPSymlink Operation = "sftp_symlink"

	OpWSMessage Operation = "ws_message"
	OpWSClose   Operation = "ws_close"

	// OpOther is parsed but not a verb the policy vocabulary classifies.
	OpOther Operation = "other"
	// OpUnknown means the statement could not be read; a rule naming it
	// fails closed.
	OpUnknown Operation = "unknown"
)

// Access says whether a statement reads a relation or changes it.
type Access string

const (
	AccessRead  Access = "read"
	AccessWrite Access = "write"
)

// Relation is one relation a statement touches, and how. The guest
// lowercases names.
type Relation struct {
	Name   string `json:"name"`
	Access Access `json:"access"`
}

// Column is one field of a result set.
type Column struct {
	Name        string `json:"name"`
	DataTypeOID uint32 `json:"data_type_oid,omitempty"`
}

// ResultDetail is the shape of a result set, without the values.
type ResultDetail struct {
	Columns   []Column `json:"columns,omitempty"`
	RowCount  int      `json:"row_count"`
	Truncated bool     `json:"truncated,omitempty"`
}

// Statement is one inspected unit of work: the document a policy
// evaluates. Field names are inspect.Statement's. There is no Protocol:
// the host fills it and refuses a guest that sets it.
//
// A codec may leave Direction empty on a decoded statement; the host
// fills it with the direction of the decode call.
type Statement struct {
	Direction Direction         `json:"direction,omitempty"`
	Text      string            `json:"text"`
	Operation Operation         `json:"operation"`
	Effects   []Operation       `json:"effects,omitempty"`
	Relations []Relation        `json:"relations,omitempty"`
	Tables    []string          `json:"tables,omitempty"`
	Database  string            `json:"database,omitempty"`
	Result    *ResultDetail     `json:"result,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// MetadataSQLIncomplete is the key under which WithAnalysis records why a
// scan could not finish; the same key libhoop's codecs use.
const MetadataSQLIncomplete = "sql.incomplete"

// WithAnalysis copies what the host lexer found onto the statement. An
// incomplete scan leaves Operation unknown and says why in Metadata.
func (s Statement) WithAnalysis(a SQLAnalysis) Statement {
	s.Operation = a.Operation
	s.Effects = a.Effects
	s.Relations = a.Relations
	s.Tables = a.Tables
	if !a.Complete {
		s.Operation = OpUnknown
		s = s.WithMetadata(MetadataSQLIncomplete, a.Reason)
	}
	return s
}

// WithMetadata adds one entry. Keys are "<protocol>.<name>".
func (s Statement) WithMetadata(key, value string) Statement {
	m := make(map[string]string, len(s.Metadata)+1)
	for k, v := range s.Metadata {
		m[k] = v
	}
	m[key] = value
	s.Metadata = m
	return s
}

// SQLAnalysis is what the host lexer found; see AnalyzeSQL.
type SQLAnalysis struct {
	Operation Operation   `json:"operation"`
	Effects   []Operation `json:"effects"`
	Relations []Relation  `json:"relations"`
	Tables    []string    `json:"tables"`
	Complete  bool        `json:"complete"`
	Reason    string      `json:"reason"`
}

// Decoded is what Codec.Decode hands back for one chunk of input.
type Decoded struct {
	Statements []Statement
	// Consumed counts bytes of the input the codec finished with. Stop at
	// the first byte of an incomplete trailing message; the host passes it
	// again, prefixed to the next read.
	Consumed int
}

// Rewritten is what Rewriter.Rewrite and Flush hand back.
type Rewritten struct {
	// Bytes to forward; may be empty while rows are held.
	Bytes []byte
	// Cells counts the values mask returned changed; a value handed over
	// and returned as-is is not masked, and the audit trail reports this.
	// Rows counts the rows with at least one changed value.
	Cells, Rows int
}

// Content is what ContentRenderer renders for the AI analyzer.
type Content struct {
	Text string
	// CacheKey names the statement's shape with literals stripped, so one
	// verdict serves repeats. Empty means never cache.
	CacheKey string
}

// Capability is an optional export group, announced in the manifest.
type Capability string

const (
	CapDeny       Capability = "deny"
	CapFilter     Capability = "filter"
	CapRewrite    Capability = "rewrite"
	CapCredential Capability = "credential"
	CapContent    Capability = "content"
)

// OptionSpec is one per-listener setting the control plane form renders
// and Open receives as a string.
type OptionSpec struct {
	Name    string   `json:"name"`
	Label   string   `json:"label,omitempty"`
	Type    string   `json:"type"`
	Values  []string `json:"values,omitempty"`
	Default string   `json:"default,omitempty"`
	Help    string   `json:"help,omitempty"`
}

// ABIVersion is the ABI this SDK speaks.
const ABIVersion = 1

// Manifest is what describe returns. Serve sets ABI; the rest is the
// plug-in's to fill. Capabilities must name the same set as the build
// tags the build turned on, and describe traps when they differ.
type Manifest struct {
	ABI              int          `json:"abi"`
	Protocol         string       `json:"protocol"`
	Label            string       `json:"label"`
	Version          string       `json:"version,omitempty"`
	Capabilities     []Capability `json:"capabilities,omitempty"`
	SQLDialect       string       `json:"sql_dialect,omitempty"`
	MaxReassembly    int          `json:"max_reassembly,omitempty"`
	Instances        string       `json:"instances,omitempty"`
	WASI             bool         `json:"wasi,omitempty"`
	CallTimeoutMS    int          `json:"call_timeout_ms,omitempty"`
	MemoryLimitPages int          `json:"memory_limit_pages,omitempty"`
	Options          []OptionSpec `json:"options,omitempty"`
}

// The wire forms the exports render; a codec never builds these.

type decodeResult struct {
	Statements []Statement `json:"statements"`
	Consumed   int         `json:"consumed"`
}

type errorResult struct {
	Error string `json:"error"`
}

type rewriteResult struct {
	Bytes string `json:"bytes"`
	Cells int    `json:"cells"`
	Rows  int    `json:"rows"`
}

type credentialResult struct {
	OK         bool       `json:"ok"`
	Credential string     `json:"credential,omitempty"`
	Statement  *Statement `json:"statement,omitempty"`
}

type contentResult struct {
	OK       bool   `json:"ok"`
	Text     string `json:"text,omitempty"`
	CacheKey string `json:"cache_key,omitempty"`
}
