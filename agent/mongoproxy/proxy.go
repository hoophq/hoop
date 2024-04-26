package mongoproxy

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/url"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/mongotypes"
)

var (
	errConnectionClose = fmt.Errorf("connection closed")
	ConnIDContextKey   struct{}
	SIDContextKey      struct{}
)

func clientErrorF(format string, a ...any) clientErrType { return fmt.Errorf(format, a...) }

type (
	onRunErrFnType func(errMsg string)
	clientErrType  error
)

type proxy struct {
	ctx              context.Context
	host             string
	port             string
	username         string
	password         string
	pid              uint32
	serverRW         io.ReadWriteCloser
	clientW          io.ReadWriter
	cancelFn         context.CancelFunc
	clientInitBuffer io.ReadWriter
	initialized      bool
	closed           bool
	connectionID     string
	sid              string
}

func New(ctx context.Context, connStr *url.URL, serverRW io.ReadWriteCloser, clientW io.ReadWriter) *proxy {
	if connStr == nil {
		connStr = &url.URL{}
	}

	cancelCtx, cancelFn := context.WithCancel(ctx)
	passwd, _ := connStr.User.Password()
	// sslMode := sslModeType(connStr.Query().Get("sslmode"))
	// if sslMode == "" {
	// 	sslMode = sslModePrefer
	// }

	return &proxy{
		ctx:              cancelCtx,
		host:             connStr.Hostname(),
		port:             connStr.Port(),
		username:         connStr.User.Username(),
		password:         passwd,
		pid:              0,
		serverRW:         serverRW,
		clientW:          clientW,
		clientInitBuffer: newBlockingReader(),
		cancelFn:         cancelFn,
		connectionID:     fmt.Sprintf("%v", ctx.Value(ConnIDContextKey)),
		sid:              fmt.Sprintf("%v", ctx.Value(SIDContextKey)),
	}
}

func (p *proxy) initalizeConnection() error {
	if p.username == "" || p.password == "" {
		return fmt.Errorf("missing password or username")
	}
	log.Infof("initializing mongo session, user=%v, sslmode=none", p.username)
	pkt, err := p.processPacket(p.clientInitBuffer)
	if err != nil {
		return fmt.Errorf("failed reading initial packet from client, err=%v", err)
	}
	conn, ok := p.serverRW.(net.Conn)
	if !ok {
		return fmt.Errorf("server is not a net.Conn type")
	}
	tlsConn := tls.Client(conn, &tls.Config{
		InsecureSkipVerify: false,
		ServerName:         p.host,
	})
	if err := tlsConn.Handshake(); err != nil {
		if verr, ok := err.(tls.RecordHeaderError); ok {
			return fmt.Errorf("tls handshake error=%v, message=%v, record-header=%X",
				verr.Msg, verr.Error(), verr.RecordHeader[:])
		}
		return fmt.Errorf("handshake error: %v", err)
	}
	if tlsConn != nil {
		p.serverRW = tlsConn
	}
	bypass, err := p.handleServerAuth(pkt)
	if err != nil {
		return err
	}
	if bypass {
		log.Infof("bypassing packet")
		pkt.Dump()
		if _, err := p.serverRW.Write(pkt.Encode()); err != nil {
			return fmt.Errorf("failed bypassing packet, err=%v", err)
		}
		return nil
	}
	return nil
	// authentication was handled, returns ok to the client!
	// return p.handleClientAuth(pkt)
}

func (p *proxy) processPacket(data io.Reader) (pkt *mongotypes.Packet, err error) {
	pkt, err = mongotypes.Decode(data)
	return pkt, p.parseIOError(err)
}

// Run start the prox by offloading the authentication and the tls with the postgres server
func (p *proxy) Run(onErrCallback onRunErrFnType) *proxy {
	if onErrCallback == nil {
		onErrCallback = func(errMsg string) {} // noop callback
	}
	initCh := make(chan error)
	go func() { initCh <- p.initalizeConnection() }()
	go func() {
		if err := <-initCh; err != nil {
			errMsg := fmt.Sprintf("failed initializing connection, reason=%v", err)
			log.Warn(errMsg)
			defer p.Close()
			close(initCh)
			// if _, ok := err.(clientErrType); ok {
			// 	_, _ = p.clientW.Write(pgtypes.NewFatalError(errMsg).Encode())
			// 	return
			// }
			onErrCallback(errMsg)
			return
		}
		p.initialized = true

		log.Infof("initialized mongo session with success")
		defer p.Close()
	exit:
		for {
			select {
			case <-p.Done():
				log.Infof("context done, err=%v", p.ctx.Err())
				break exit
			default:
				pkt, err := p.processPacket(p.serverRW)
				if err != nil {
					if err != errConnectionClose {
						errMsg := fmt.Sprintf("failed processing packet, reason=%v", err)
						log.Warn(errMsg)
						onErrCallback(errMsg)
					}
					break exit
				}
				_, err = p.clientW.Write(pkt.Encode())
				if err != nil {
					errMsg := fmt.Sprintf("failed writing packet to stream, reason=%v", err)
					log.Warn(errMsg)
					onErrCallback(errMsg)
					break exit
				}
				fmt.Printf("server read connid=%v ---->>\n", p.connectionID)
				pkt.Dump()
			}
		}
		log.Infof("done reading connection=%v", p.connectionID)
	}()
	return p
}

func (p *proxy) Done() <-chan struct{} { return p.ctx.Done() }
func (p *proxy) Close() error {
	p.closed = true
	p.cancelFn()
	return p.serverRW.Close()
}

func (p *proxy) Write(b []byte) (n int, err error) {
	if !p.initialized {
		log.Infof("writing to init buffer, %v", len(b))
		return p.clientInitBuffer.Write(b)
	}
	pkt, err := p.processPacket(bytes.NewBuffer(b))
	if err != nil || pkt == nil {
		return
	}
	fmt.Printf("server write connid=%v ---->>\n", p.connectionID)
	pkt.Dump()
	n, err = p.serverRW.Write(pkt.Encode())

	return
}

func (p *proxy) parseIOError(err error) error {
	// the connection was closed, ignore any errors
	if p.closed {
		return errConnectionClose
	}
	if err == io.EOF {
		return errConnectionClose
	}
	return err
}
