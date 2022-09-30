package exec

import "syscall"

const SIGWINCH = syscall.Signal(28)

var (
	TermEnterKeyStrokeType = []byte{10}
	UnknowExecExitCode     = 254
	InternalErroExitCode   = 254
)
