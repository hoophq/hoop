package daemon

import (
	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	codecssh "github.com/hoophq/libhoop/v2/codec/ssh"
)

// Statement metadata keys an ssh lane sets. They reach OPA's input document
// and the audit trail, so the spellings are a contract.
const (
	// MetadataSSHCapability names the surface a statement came from:
	// exec, env or sftp. It is the one fact that is not derivable from the
	// operation for a reader who does not know the twelve names by heart.
	MetadataSSHCapability = "ssh.capability"

	// MetadataSSHEnvValue carries the value of an environment variable a
	// client asked to set. The NAME is the statement's text, because that is
	// what a rule matches; the value travels beside it so the audit trail
	// records what was actually set.
	//
	// Recorded raw in v1, the way a SQL statement's literal parameters are.
	// Masking has no response to act on here, so there is nothing to rewrite
	// in flight — but a sink configured to redact statement text fingerprints
	// this too, which is why the spelling is defined in audit and aliased
	// here rather than written twice.
	MetadataSSHEnvValue = audit.MetadataSSHEnvValue

	// MetadataSSHPath is the file path an sftp operation names, duplicated
	// out of the statement text so a Rego rule can read it as a field rather
	// than re-deriving it.
	MetadataSSHPath = "ssh.path"

	// MetadataSSHTarget is the second path of a two-path request: the
	// destination of a rename, the link name of a symlink. Absent otherwise.
	MetadataSSHTarget = "ssh.target"

	// MetadataSSHPathEnd says which end of a two-path request this
	// statement is, "source" or "target". Both ends are evaluated, so
	// without this the audit trail shows one operation twice with no way to
	// tell the rows apart.
	MetadataSSHPathEnd = "ssh.path_end"
)

// sshStatements builds the statements one connection reports.
//
// What a statement's TEXT is depends on the operation, and that is the thing
// to keep in view when reading a rule: a command line for exec_line, a
// variable name for env_set, a path for every sftp_*. A pattern_match rule
// scopes itself with `operations` the way it does on any other lane, and the
// ADR's worked examples do exactly that.
//
// There is no Tables and no Access on any of them. SSH has no relations, and
// inventing one from a path would make a `table` rule match by accident;
// policy refuses that rule type on this lane for the same reason.
//
// It holds no state. The lane name and the identity live on the session, and
// the audit trail reads them from there; carrying a second copy here would be
// a field that can disagree with the one the record is actually built from.
type sshStatements struct{}

// exec is the whole command line, as one statement. The protocol gives the
// boundary — an exec request carries exactly one command string — so nothing
// here has to guess where a statement begins.
func (s sshStatements) exec(cmd string) inspect.Statement {
	return inspect.Statement{
		Protocol:  inspect.SSH,
		Direction: inspect.FromClient,
		Operation: inspect.OpExecLine,
		Text:      cmd,
		Metadata: map[string]string{
			MetadataSSHCapability: string(codecssh.CapExec),
		},
	}
}

// envSet matches on the NAME. A rule fencing LD_PRELOAD is written against
// the variable, not against whatever an attacker puts in it.
func (s sshStatements) envSet(name, value string) inspect.Statement {
	return inspect.Statement{
		Protocol:  inspect.SSH,
		Direction: inspect.FromClient,
		Operation: inspect.OpEnvSet,
		Text:      name,
		Metadata: map[string]string{
			MetadataSSHCapability: string(codecssh.CapEnv),
			MetadataSSHEnvValue:   value,
		},
	}
}

// sftp is one file operation against one path.
//
// A two-path request produces TWO statements, one per end, and the caller
// evaluates both: a rename out of a fenced directory and a rename into one
// are different questions, and a rule that only ever saw the source would
// miss the second. end names which is which so the audit trail can tell the
// rows apart.
func (s sshStatements) sftp(op inspect.Operation, path, target, end string) inspect.Statement {
	meta := map[string]string{
		MetadataSSHCapability: string(codecssh.CapSFTP),
		MetadataSSHPath:       path,
	}
	if target != "" {
		meta[MetadataSSHTarget] = target
	}
	if end != "" {
		meta[MetadataSSHPathEnd] = end
	}
	return inspect.Statement{
		Protocol:  inspect.SSH,
		Direction: sftpDirection(op),
		Operation: op,
		Text:      path,
		Metadata:  meta,
	}
}

// The two ends of a two-path request.
const (
	sshPathEndSource = "source"
	sshPathEndTarget = "target"
)

// sftpDirection reports which way the bytes of an operation travel.
//
// Only a read moves bytes to the client; everything else is the client
// acting on the filesystem. It is reported so an audit query can separate
// "what did they take" from "what did they change" without knowing the
// twelve operation names.
func sftpDirection(op inspect.Operation) inspect.Direction {
	if op == inspect.OpSFTPRead {
		return inspect.FromServer
	}
	return inspect.FromClient
}
