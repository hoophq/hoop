package types

// Severity represents the severity of a thrown error. The possible error
// severities are ERROR, FATAL, or PANIC (in an error message), or WARNING,
// NOTICE, DEBUG, INFO, or LOG (in a notice message)
type Severity string

type Code string

// Represents the severity of a thrown error. The possible error severities are
// ERROR, FATAL, or PANIC (in an error message), or WARNING, NOTICE, DEBUG,
// INFO, or LOG (in a notice message)
const (
	LevelError   Severity = "ERROR"
	LevelFatal   Severity = "FATAL"
	LevelPanic   Severity = "PANIC"
	LevelWarning Severity = "WARNING"
	LevelNotice  Severity = "NOTICE"
	LevelDebug   Severity = "DEBUG"
	LevelInfo    Severity = "INFO"
	LevelLog     Severity = "LOG"
)

// Possible values are 'I' if idle (not in a transaction block); 'T' if in a
// transaction block; or 'E' if in a failed transaction block
// (queries will be rejected until block is ended).
const (
	ServerIdle              = 'I'
	ServerTransactionBlock  = 'T'
	ServerTransactionFailed = 'E'
)

// http://www.postgresql.org/docs/9.5/static/errcodes-appendix.html.
const (
	// Class 08 - Connection Exception
	ConnectionFailure Code = "08006"
	// Class 0A — Feature Not Supported
	FeatureNotSupported Code = "0A000"
	// Class 28 — Invalid Authorization Specification
	InvalidPassword                   Code = "28P01"
	InvalidAuthorizationSpecification Code = "28000"
)
