package plugintypes

import (
	"fmt"
	"runtime"
)

type baseErr struct {
	message string
	caller  string
}

type InternalError struct {
	internalErr error
	baseErr
}

type InvalidArgErr struct {
	baseErr
}

func (e *baseErr) Error() string { return e.message }

func (e *InternalError) FullErr() string {
	return fmt.Sprintf("message=%v, err=%v, caller=%v", e.message, e.internalErr, e.caller)
}

func (e *InternalError) HasInternalErr() bool { return e.internalErr != nil }

func InternalErr(msg string, internalErr error) *InternalError {
	_, file, _, _ := runtime.Caller(1)
	return &InternalError{
		internalErr: internalErr,
		baseErr:     baseErr{message: fmt.Sprintf("internal error, %s", msg), caller: file}}
}

func InvalidArgument(format string, a ...any) *InvalidArgErr {
	_, file, _, _ := runtime.Caller(1)
	return &InvalidArgErr{baseErr: baseErr{message: fmt.Sprintf(format, a...), caller: file}}
}
