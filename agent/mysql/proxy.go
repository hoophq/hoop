package mysql

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"io"

	"github.com/runopsio/hoop/agent/mysql/types"
	"github.com/runopsio/hoop/common/log"
)

type (
	Reader interface {
		ReadByte() (byte, error)
		Read(p []byte) (int, error)
	}
	NextFn       func()
	MiddlewareFn func(nextFn NextFn, pkt *types.Packet, cli, srv io.WriteCloser)
	Proxy        interface {
		Run() Proxy
		Write(b []byte) (int, error)
		Done() <-chan struct{}
		Close() error
	}
)

type proxy struct {
	ctx          context.Context
	serverWriter io.WriteCloser
	clientWriter io.WriteCloser
	serverReader Reader
	middlewares  []MiddlewareFn
	cancelFn     context.CancelFunc
}

func NewProxy(ctx context.Context, server io.ReadWriteCloser, client io.WriteCloser, fns ...MiddlewareFn) Proxy {
	cancelCtx, cancelFn := context.WithCancel(ctx)
	return &proxy{
		ctx:          cancelCtx,
		serverWriter: server,
		serverReader: bufio.NewReader(server),
		clientWriter: client,
		middlewares:  fns,
		cancelFn:     cancelFn}
}

func (r *proxy) processPacket(source types.SourceType, data Reader) (pkt *types.Packet, err error) {
	pkt, err = decodePacket(data)
	if err != nil {
		return
	}
	pkt.Source = source
	for _, middlewareFn := range r.middlewares {
		processNextMiddleware := false
		middlewareFn(func() { processNextMiddleware = true }, pkt, r.clientWriter, r.serverWriter)
		if !processNextMiddleware {
			// if next is not called, don't process the default action
			// to write the packet and stop processing further middlewares
			return nil, nil
		}
	}
	return
}

// Run reads packets in a goroutine of the server and writes back to client
func (r *proxy) Run() Proxy {
	go func() {
	exit:
		for i := 1; ; i++ {
			select {
			case <-r.Done():
				log.Infof("context done, err=%v", r.ctx.Err())
				break exit
			default:
				pkt, err := r.processPacket(types.SourceServer, r.serverReader)
				if err != nil {
					if err != io.EOF {
						log.Infof("failed processing packet, err=%v", err)
					}
					break exit
				}
				if pkt != nil {
					if _, err := r.clientWriter.Write(pkt.Encode()); err != nil {
						log.Warnf("failed writing packet out, err=%v", err)
						break exit
					}
				}
			}
		}
		_ = r.Close()
		log.Infof("done reading")
	}()
	return r
}

func (r *proxy) Close() error          { r.cancelFn(); return nil }
func (r *proxy) Done() <-chan struct{} { return r.ctx.Done() }
func (r *proxy) Write(b []byte) (int, error) {
	pkt, err := r.processPacket(types.SourceClient, bytes.NewBuffer(b))
	if err != nil {
		return 0, err
	}
	if pkt == nil {
		return 0, nil
	}
	return r.serverWriter.Write(pkt.Encode())
}

// https://dev.mysql.com/doc/dev/mysql-server/latest/page_protocol_basic_packets.html
func decodePacket(data Reader) (*types.Packet, error) {
	p := &types.Packet{}
	var header [4]byte
	_, err := io.ReadFull(data, header[:3])
	if err != nil {
		return nil, err
	}
	sequenceID, err := data.ReadByte()
	if err != nil {
		return nil, err
	}
	pktLen := binary.LittleEndian.Uint32(header[:])
	p.SetSeq(sequenceID)
	p.Frame = make([]byte, pktLen)
	_, err = io.ReadFull(data, p.Frame)
	return p, err
}
