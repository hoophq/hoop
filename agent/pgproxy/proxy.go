package pgproxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"

	"github.com/runopsio/hoop/agent/dlp"
	"github.com/runopsio/hoop/common/log"
	pgtypes "github.com/runopsio/hoop/common/pgtypes"
	"github.com/xo/dburl"
)

// type Proxy interface {
// 	Run(onRunErrFnType) Proxy
// 	Write(b []byte) (int, error)
// 	WithDataLossPrevention(dlpClient dlp.Client, infoTypes []string) Proxy
// 	Done() <-chan struct{}
// 	Close() error
// }

type clientErrType error

func clientErrorF(format string, a ...any) clientErrType { return fmt.Errorf(format, a...) }

type onRunErrFnType func(errMsg string)

type proxy struct {
	ctx              context.Context
	tlsConfig        *tlsConfig
	username         string
	password         string
	serverRW         io.ReadWriteCloser
	clientW          io.Writer
	cancelFn         context.CancelFunc
	clientInitBuffer io.ReadWriter
	initialized      bool

	dlp *dlpHandler
}

func New(ctx context.Context, connStr *dburl.URL, serverRW io.ReadWriteCloser, clientW io.Writer) *proxy {
	if connStr == nil {
		connStr = &dburl.URL{}
	}
	cancelCtx, cancelFn := context.WithCancel(ctx)
	passwd, _ := connStr.User.Password()
	sslMode := sslModeType(connStr.Query().Get("sslmode"))
	if sslMode == "" {
		sslMode = sslModePrefer
	}
	return &proxy{
		ctx: cancelCtx,
		tlsConfig: &tlsConfig{
			sslMode:      sslMode,
			serverName:   connStr.Hostname(),
			rootCertPath: connStr.Query().Get("sslrootcert"),
		},
		dlp:              &dlpHandler{},
		username:         connStr.User.Username(),
		password:         passwd,
		serverRW:         serverRW,
		clientW:          clientW,
		clientInitBuffer: newBlockingReader(),
		cancelFn:         cancelFn}
}

func (p *proxy) WithDataLossPrevention(dlpClient dlp.Client, infoTypes []string) *proxy {
	p.dlp = newDlpHandler(dlpClient, p.clientW, infoTypes)
	return p
}

func (p *proxy) initalizeConnection() error {
	if p.username == "" || p.password == "" {
		return fmt.Errorf("missing password or username")
	}
	log.Infof("initializing postgres session, user=%v, sslmode=%v, servername=%v",
		p.username, p.tlsConfig.sslMode, p.tlsConfig.serverName)
	_, pkt, err := pgtypes.DecodeStartupPacket(p.clientInitBuffer)
	if err != nil {
		return fmt.Errorf("failed decoding startup packet, err=%v", err)
	}
	if pkt.IsFrontendSSLRequest() {
		_, _ = p.clientW.Write([]byte{pgtypes.ServerSSLNotSupported.Byte()})
		_, pkt, err = pgtypes.DecodeStartupPacket(p.clientInitBuffer)
		if err != nil {
			return fmt.Errorf("failed decoding startup packet (tls), err=%v", err)
		}
	}
	if pkt.IsCancelRequest() {
		return fmt.Errorf("cancel request is not implemented")
	}

	startupMessage, err := pgtypes.DecodeStartupPacketWithUsername(bytes.NewBuffer(pkt.Encode()), p.username)
	if err != nil {
		return fmt.Errorf("failed decoding startup packet with username, err=%v", err)
	}
	log.Infof("writing ssl request")
	sslRequest := pgtypes.NewSSLRequestPacket()
	if _, err := p.serverRW.Write(sslRequest[:]); err != nil {
		return fmt.Errorf("failed writing ssl request, err=%v", err)
	}

	b := make([]byte, 1)
	if _, err := p.serverRW.Read(b); err != nil {
		return fmt.Errorf("failed reading ssl request response, err=%v", err)
	}
	conn, ok := p.serverRW.(net.Conn)
	if !ok {
		return fmt.Errorf("server is not a net.Conn type")
	}
	serverSupportsTLS := b[0] == 'S'
	tlsConn, err := p.tlsClientHandshake(conn, serverSupportsTLS)
	if err != nil {
		return err
	}
	if tlsConn != nil {
		p.serverRW = tlsConn
	}
	log.Infof("sslmode=%v, server-supports-tls=%v, encrypted=%v",
		p.tlsConfig.sslMode, serverSupportsTLS, tlsConn != nil)

	if err := p.handleAuth(startupMessage); err != nil {
		return err
	}
	p.initialized = true
	return nil
}

func (p *proxy) processPacket(data io.Reader) (pkt *pgtypes.Packet, err error) {
	_, pkt, err = pgtypes.DecodeTypedPacket(data)
	return
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
			defer p.serverRW.Close()
			close(initCh)
			if _, ok := err.(clientErrType); ok {
				_, _ = p.clientW.Write(pgtypes.NewFatalError(errMsg).Encode())
				return
			}
			onErrCallback(errMsg)
			return
		}
		p.initialized = true
		log.Infof("initialized postgres session with success")
		defer p.serverRW.Close()
	exit:
		for {
			select {
			case <-p.Done():
				log.Infof("context done, err=%v", p.ctx.Err())
				break exit
			default:
				pkt, err := p.processPacket(p.serverRW)
				if err != nil {
					if err != io.EOF {
						errMsg := fmt.Sprintf("failed processing packet, reason=%v", err)
						log.Warn(errMsg)
						onErrCallback(errMsg)
					}
					break exit
				}
				err = p.dlp.handle(pkt)
				switch err {
				// it will handle onlt if dlp is enabled.
				// A noop error indicates that the packet was not handled
				// and must be forwarded to the client
				case errDLPNoop:
					if _, err := p.clientW.Write(pkt.Encode()); err != nil {
						errMsg := fmt.Sprintf("failed writing packet to stream, reason=%v", err)
						log.Warn(errMsg)
						onErrCallback(errMsg)
						break exit
					}
				// it means that the packet was handled by the dlp handler
				case nil:
				default:
					errMsg := fmt.Sprintf("failed writing packet to stream, reason=%v", err)
					log.Warn(errMsg)
					onErrCallback(errMsg)
					break exit
				}
			}
		}
		log.Infof("done reading connection")
	}()
	return p
}

func (p *proxy) Done() <-chan struct{} { return p.ctx.Done() }
func (p *proxy) Close() error {
	p.cancelFn()
	return nil
}

func (p *proxy) Write(b []byte) (n int, err error) {
	if !p.initialized {
		return p.clientInitBuffer.Write(b)
	}
	pkt, err := p.processPacket(bytes.NewBuffer(b))
	if err != nil || pkt == nil {
		return
	}
	if pkt.IsCancelRequest() {
		log.Infof("got cancel request!!")
	}
	return p.serverRW.Write(pkt.Encode())
}
