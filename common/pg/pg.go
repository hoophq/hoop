package pg

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
)

type (
	contextKey     int
	ResponseWriter io.WriteCloser
	Reader         interface {
		ReadByte() (byte, error)
		Read(p []byte) (int, error)
	}
	NextFn       func()
	MiddlewareFn func(nextFn NextFn, pkt *Packet, w ResponseWriter)
	Proxy        interface {
		Run() Proxy
		RunWithReader(pgClientReader Reader) Proxy
		Write(b []byte) (int, error)
		Send(b []byte) error
		Done() <-chan struct{}
		Cancel()
		Close() error
	}
)

var ErrNoop = errors.New("NOOP")

const (
	DefaultBufferSize              = 1 << 24 // 16777216 bytes
	sessionIDContextKey contextKey = iota
)

type proxy struct {
	ctx         context.Context
	w           ResponseWriter
	packetChan  *chan []byte
	middlewares []MiddlewareFn
	cancelFn    context.CancelFunc
	sessionID   any
	err         error
}

func NewProxy(ctx context.Context, w ResponseWriter, fns ...MiddlewareFn) Proxy {
	sessionID := ctx.Value(sessionIDContextKey)
	cancelCtx, cancelFn := context.WithCancel(ctx)
	return &proxy{
		ctx:         cancelCtx,
		w:           w,
		middlewares: fns,
		sessionID:   sessionID,
		cancelFn:    cancelFn}
}

func NewContext(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, sessionIDContextKey, sessionID)
}

func (r *proxy) processPacket(data Reader) error {
	_, pkt, err := DecodeTypedPacket(data)
	if err != nil {
		return err
	}
	for _, middlewareFn := range r.middlewares {
		processNextMiddleware := false
		middlewareFn(func() { processNextMiddleware = true }, pkt, r.w)
		if !processNextMiddleware {
			// if next is not called, don't process the default action
			// to write the packet and stop processing further middlewares
			return nil
		}
	}
	_, err = r.Write(pkt.Encode())
	return err
}

func (r *proxy) RunWithReader(pgClientReader Reader) Proxy {
	log.Printf("session=%v | pgrw - started", r.sessionID)
	go func() {
	exit:
		for {
			select {
			case <-r.Done():
				log.Printf("session=%v | pgrw - context done, err=%v", r.sessionID, r.ctx.Err())
				break exit
			default:
				if err := r.processPacket(pgClientReader); err != nil {
					if err != io.EOF {
						log.Printf("session=%v | pgrw - failed processing packet, err=%v", r.sessionID, err)
					}
					break exit
				}
			}
		}
		r.Cancel()
		log.Printf("session=%v | pgrw - done reading, err=%v", r.sessionID, r.err)
	}()
	return r
}

func (r *proxy) Run() Proxy {
	log.Printf("session=%v | chanpgrw - started", r.sessionID)
	packetChan := make(chan []byte)
	r.packetChan = &packetChan
	go func() {
	exit:
		for {
			select {
			case <-r.ctx.Done():
				log.Printf("session=%v | chanpgrw - context done, err=%v", r.sessionID, r.ctx.Err())
				break exit
			case rawPkt := <-packetChan:
				data := bufio.NewReaderSize(bytes.NewBuffer(rawPkt), len(rawPkt))
				if err := r.processPacket(data); err != nil {
					if err != io.EOF {
						log.Printf("session=%v | chanpgrw - failed processing packet, err=%v", r.sessionID, err)
					}
					break exit
				}
			}
		}
		r.Cancel()
		log.Printf("session=%v | chanpgrw - done reading", r.sessionID)
	}()
	return r
}

func (r *proxy) Done() <-chan struct{} {
	return r.ctx.Done()
}

func (r *proxy) Cancel() {
	r.cancelFn()
}

// Closes the ResponseWriter if it's set
func (r *proxy) Close() error {
	if r.packetChan != nil {
		close(*r.packetChan)
	}
	if r.w != nil {
		return r.w.Close()
	}
	return nil
}

// Write writes b directly to the pg.ResponseWriter object
func (r *proxy) Write(b []byte) (int, error) {
	return r.w.Write(b)
}

// Send sends b to be processed by the defined middlewares
func (r *proxy) Send(b []byte) error {
	if r.packetChan != nil {
		select {
		case <-r.Done():
			log.Printf("session=%v | chanpgrw-send - context done, err=%v", r.sessionID, r.ctx.Err())
		case *r.packetChan <- b:
		default:
			return fmt.Errorf("channel is not available")
		}
		return nil
	}
	return ErrNoop
}
