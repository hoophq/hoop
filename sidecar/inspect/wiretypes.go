package inspect

import codectypes "github.com/hoophq/libhoop/v2/codec/types"

// The wire vocabulary is DEFINED in libhoop and aliased here.
//
// libhoop is a leaf: it imports nothing from this repository, because the
// dependency runs the other way — sidecar and the hoop gateway both
// consume libhoop. The codecs live there, and a codec cannot return a type it
// is forbidden to import.
//
// Aliases rather than a conversion layer. `type Statement = ...` is the SAME
// type, not a copy, so a libhoop codec satisfies the Codec interface below
// structurally without naming this package, and no translation runs per
// message. The alternative — sidecar declaring its own structs and
// converting at the boundary — costs an allocation per decoded statement on
// the proxied-query hot path and, worse, leaves two definitions of the
// document a policy evaluates. A field that drifts between them is a policy
// that silently stops matching.
//
// These names are sidecar's public API. Changing one means changing
// libhoop's types package; the compiler will tell you, because there is only
// one definition.
type (
	Protocol      = codectypes.Protocol
	Direction     = codectypes.Direction
	Operation     = codectypes.Operation
	Access        = codectypes.Access
	Relation      = codectypes.Relation
	Statement     = codectypes.Statement
	ResultDetail  = codectypes.ResultDetail
	ReframeResult = codectypes.ReframeResult
	Column        = codectypes.Column
	HTTPDetail    = codectypes.HTTPDetail
	SQLAnalysis   = codectypes.SQLAnalysis
)

const (
	Postgres = codectypes.Postgres
	MSSQL    = codectypes.MSSQL
	MySQL    = codectypes.MySQL
	MongoDB  = codectypes.MongoDB
	HTTP     = codectypes.HTTP
	// GRPC is canonical in libhoop like every other protocol, but unlike
	// them it has no codec to register: the lane terminates HTTP/2 and
	// enters at parsed statements (ADR-0013).
	GRPC = codectypes.GRPC
	// Spanner shares GRPC's transport and its no-codec exception: the lane
	// terminates HTTP/2 through the same machinery and differs only in
	// treating the payloads as carriers of GoogleSQL.
	Spanner = codectypes.Spanner
	// SSH has no codec either, for a third reason. GRPC and Spanner have
	// bytes a decoder could read and enter above them anyway; SSH has
	// none. The connection is encrypted end to end, so a component in the
	// middle can read nothing, and the lane must BE one end of it. A
	// registry position handed relay bytes would be handed ciphertext
	// (ADR-0015).
	SSH = codectypes.SSH

	FromClient = codectypes.FromClient
	FromServer = codectypes.FromServer

	OpSelect   = codectypes.OpSelect
	OpInsert   = codectypes.OpInsert
	OpUpdate   = codectypes.OpUpdate
	OpDelete   = codectypes.OpDelete
	OpCreate   = codectypes.OpCreate
	OpDrop     = codectypes.OpDrop
	OpAlter    = codectypes.OpAlter
	OpTruncate = codectypes.OpTruncate
	OpGrant    = codectypes.OpGrant
	OpRevoke   = codectypes.OpRevoke
	OpCall     = codectypes.OpCall
	OpShow     = codectypes.OpShow
	OpSet      = codectypes.OpSet
	OpBegin    = codectypes.OpBegin
	OpCommit   = codectypes.OpCommit
	OpRollback = codectypes.OpRollback

	OpGet     = codectypes.OpGet
	OpPost    = codectypes.OpPost
	OpPut     = codectypes.OpPut
	OpPatch   = codectypes.OpPatch
	OpHead    = codectypes.OpHead
	OpOptions = codectypes.OpOptions
	OpConnect = codectypes.OpConnect
	OpTrace   = codectypes.OpTrace

	// SSH operations. The capability is the prefix, so a rule reads which
	// surface it fences without SSH needing a channel field of its own.
	// There is no sftp_open: a client's open surfaces as the first read or
	// write against the path, which is the earliest point a rule can act.
	OpExecLine = codectypes.OpExecLine
	OpEnvSet   = codectypes.OpEnvSet

	OpSFTPRead    = codectypes.OpSFTPRead
	OpSFTPWrite   = codectypes.OpSFTPWrite
	OpSFTPRemove  = codectypes.OpSFTPRemove
	OpSFTPRename  = codectypes.OpSFTPRename
	OpSFTPMkdir   = codectypes.OpSFTPMkdir
	OpSFTPRmdir   = codectypes.OpSFTPRmdir
	OpSFTPList    = codectypes.OpSFTPList
	OpSFTPStat    = codectypes.OpSFTPStat
	OpSFTPSetstat = codectypes.OpSFTPSetstat
	OpSFTPSymlink = codectypes.OpSFTPSymlink

	OpOther   = codectypes.OpOther
	OpUnknown = codectypes.OpUnknown

	AccessRead  = codectypes.AccessRead
	AccessWrite = codectypes.AccessWrite

	// MetadataSQLIncomplete names the metadata key carrying why a scan could
	// not finish. Present only when Operation is OpUnknown for that reason.
	MetadataSQLIncomplete = codectypes.MetadataSQLIncomplete
)

// ErrStreamUnsafe means the codec recognized bytes that would take the
// connection OUTSIDE the relay's control if forwarded.
//
// A third category beside "malformed" and "denied by a rule": a well-formed
// instruction from the upstream that, honored, moves the client to a socket
// the relay does not hold. MSSQL's routing ENVCHANGE is the case that
// motivated it. A caller MUST close the connection rather than forward.
var ErrStreamUnsafe = codectypes.ErrStreamUnsafe
