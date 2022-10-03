package pg

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log"
)

type ResponseWriter io.WriteCloser
type Reader interface {
	ReadByte() (byte, error)
	Read(p []byte) (int, error)
}
type NextFn func()
type MiddlewareFn func(nextFn NextFn, pkt *Packet, w ResponseWriter)

var ErrNoop = errors.New("NOOP")

const DefaultBufferSize = 1 << 24 // 16777216 bytes

type Proxy interface {
	Run() Proxy
	RunWithReader(pgClientReader Reader) Proxy
	Write(b []byte) (int, error)
	Send(b []byte) error
	Done() <-chan struct{}
	Cancel()
	Close() error
	Error() error
}

type proxy struct {
	ctx         context.Context
	w           ResponseWriter
	packetChan  *chan []byte
	middlewares []MiddlewareFn
	cancelFn    context.CancelFunc
	err         error
}

func NewProxy(ctx context.Context, w ResponseWriter, fns ...MiddlewareFn) Proxy {
	cancelCtx, cancelFn := context.WithCancel(ctx)
	return &proxy{
		ctx:         cancelCtx,
		w:           w,
		middlewares: fns,
		cancelFn:    cancelFn}
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
	_, err = r.w.Write(pkt.Encode())
	return err
}

func (r *proxy) RunWithReader(pgClientReader Reader) Proxy {
	go func() {
	exit:
		for {
			select {
			case <-r.Done():
				log.Printf("run reader - context done, err=%v", r.ctx.Err())
				r.err = r.ctx.Err()
				break exit
			default:
				if err := r.processPacket(pgClientReader); err != nil {
					r.err = err
					break exit
				}
			}
		}
		r.cancelFn()
		log.Printf("run reader - done reading, err=%v", r.err)
	}()
	return r
}

func (r *proxy) Run() Proxy {
	packetChan := make(chan []byte)
	r.packetChan = &packetChan
	go func() {
	exit:
		for {
			select {
			case <-r.ctx.Done():
				log.Printf("run - context done, err=%v", r.ctx.Err())
				r.err = r.ctx.Err()
				break exit
			case rawPkt := <-packetChan:
				data := bufio.NewReaderSize(bytes.NewBuffer(rawPkt), len(rawPkt))
				if err := r.processPacket(data); err != nil {
					r.err = err
					break exit
				}
			}
		}
		r.cancelFn()
		log.Printf("run - done reading")
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
	if r.w != nil {
		return r.w.Close()
	}
	return nil
}

func (r *proxy) Error() error {
	return r.err
}

// Write writes b directly to the pg.ResponseWriter object
func (r *proxy) Write(b []byte) (int, error) {
	return r.w.Write(b)
}

// Send sends b to be processed by the defined middlewares
func (r *proxy) Send(b []byte) error {
	if r.packetChan != nil {
		// TODO: check if is not closed!
		*r.packetChan <- b
		return nil
	}
	return ErrNoop
}
