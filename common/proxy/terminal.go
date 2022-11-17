package proxy

import "syscall"

const SIGWINCH = syscall.Signal(28)

var (
	TermEnterKeyStrokeType = []byte{10}
	InternalErrorExitCode  = 254
)
