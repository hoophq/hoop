package errors

import (
	"context"
	"fmt"
	"runtime"

	pb "github.com/runopsio/hoop/common/proto"
)

// TODO: add grpc status code
type baseErr struct {
	message    string
	caller     string
	pluginName string
}

type NoopErr struct {
	pkt *pb.Packet
	baseErr
}

type NoopContextErr struct {
	ctx context.Context
	baseErr
}

func (e *NoopErr) Packet() *pb.Packet              { return e.pkt }
func (e *NoopContextErr) Context() context.Context { return e.ctx }

type InternalErr struct {
	internalErr error
	baseErr
}

type InvalidArgErr struct {
	baseErr
}

func (e *baseErr) Error() string {
	if e.pluginName != "" {
		return fmt.Sprintf("[plugin:%s] %s", e.pluginName, e.message)
	}
	return e.message
}

func (e *InternalErr) FullErr() string {
	return fmt.Sprintf("message=%v, err=%v, caller=%v", e.message, e.internalErr, e.caller)
}

func (e *InternalErr) HasInternalErr() bool { return e.internalErr != nil }

func Noop(pkt *pb.Packet) *NoopErr {
	_, file, _, _ := runtime.Caller(1)
	return &NoopErr{pkt: pkt, baseErr: baseErr{caller: file}}
}

// NoopContext returns a noop error with a new context
func NoopContext(ctx context.Context) *NoopContextErr {
	_, file, _, _ := runtime.Caller(1)
	return &NoopContextErr{ctx: ctx, baseErr: baseErr{caller: file}}
}

func Internal(msg string, internalErr error) *InternalErr {
	_, file, _, _ := runtime.Caller(1)
	return &InternalErr{
		internalErr: internalErr,
		baseErr:     baseErr{message: fmt.Sprintf("internal error, %s", msg), caller: file}}
}

func InvalidArgument(format string, a ...any) *InvalidArgErr {
	_, file, _, _ := runtime.Caller(1)
	return &InvalidArgErr{baseErr: baseErr{message: fmt.Sprintf(format, a...), caller: file}}
}
