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
)

var errConnectionClose = fmt.Errorf("connection closed")

type clientErrType error

func clientErrorF(format string, a ...any) clientErrType { return fmt.Errorf(format, a...) }

type onRunErrFnType func(errMsg string)

type proxy struct {
	ctx              context.Context
	tlsConfig        *tlsConfig
	host             string
	port             string
	username         string
	password         string
	pid              uint32
	serverRW         io.ReadWriteCloser
	clientW          io.Writer
	cancelFn         context.CancelFunc
	clientInitBuffer io.ReadWriter
	initialized      bool
	closed           bool

	dlp *dlpHandler
}

type Options struct {
	Hostname string
	Port     string
	Username string
	Password string
	// https://www.postgresql.org/docs/current/libpq-ssl.html#LIBPQ-SSL-PROTECTION
	// disable, prefer, require, verify-full
	SSLMode     string
	SSLRootCert string
}

func New(ctx context.Context, opts Options, serverRW io.ReadWriteCloser, clientW io.Writer) *proxy {
	cancelCtx, cancelFn := context.WithCancel(ctx)
	sslMode := sslModeType(opts.SSLMode)
	if sslMode == "" {
		sslMode = sslModePrefer
	}

	return &proxy{
		ctx: cancelCtx,
		tlsConfig: &tlsConfig{
			sslMode:      sslMode,
			serverName:   opts.Hostname,
			rootCertPath: opts.SSLRootCert,
		},
		dlp:              &dlpHandler{},
		host:             opts.Hostname,
		port:             opts.Port,
		username:         opts.Username,
		password:         opts.Password,
		pid:              0,
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
		if err := p.handleCancelRequest(pkt); err != nil {
			log.Warn(err)
		}
		return nil
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
	return nil
}

func (p *proxy) processPacket(data io.Reader) (pkt *pgtypes.Packet, err error) {
	_, pkt, err = pgtypes.DecodeTypedPacket(data)
	if err == nil && pkt.Type() == pgtypes.ServerBackendKeyData {
		keyData, err := pgtypes.NewBackendKeyData(pkt)
		if err != nil {
			log.Warnf("failed decoding BackendKeyData from server, err=%v", err)
		}
		if keyData != nil {
			log.Infof("process %v registered in proc manager", keyData.Pid)
			ProcManager().add(&procInfo{host: p.host, port: p.port, pid: keyData.Pid, secretKey: keyData.SecretKey})
			p.pid = keyData.Pid
		}
	}
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
			if _, ok := err.(clientErrType); ok {
				_, _ = p.clientW.Write(pgtypes.NewFatalError(errMsg).Encode())
				return
			}
			onErrCallback(errMsg)
			return
		}
		p.initialized = true
		log.Infof("initialized postgres session with success")
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
	ProcManager().flush(p.host, p.pid)
	p.closed = true
	p.cancelFn()
	return p.serverRW.Close()
}

func (p *proxy) Write(b []byte) (n int, err error) {
	if !p.initialized {
		return p.clientInitBuffer.Write(b)
	}
	pkt, err := p.processPacket(bytes.NewBuffer(b))
	if err != nil || pkt == nil {
		return
	}
	return p.serverRW.Write(pkt.Encode())
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
