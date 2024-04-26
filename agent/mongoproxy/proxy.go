package mongoproxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/mongotypes"
)

const defaultAuthDB = "admin"

var (
	errConnectionClose = fmt.Errorf("connection closed")
	ConnIDContextKey   struct{}
	SIDContextKey      struct{}
)

type onRunErrFnType func(errMsg string)

type proxy struct {
	ctx              context.Context
	host             string
	port             string
	username         string
	password         string
	serverRW         io.ReadWriteCloser
	clientW          io.ReadWriter
	cancelFn         context.CancelFunc
	clientInitBuffer io.ReadWriter
	initialized      bool
	tlsProxyConfig   *tlsProxyConfig
	withSrv          bool
	authSource       string
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

	optsGetter := connStr.Query().Get

	authSource := optsGetter("authSource")
	authDB := strings.TrimPrefix(connStr.Path, "/")
	switch {
	case authSource == "" && authDB == "":
		authSource = defaultAuthDB
	case authSource == "" && authDB != "":
		authSource = authDB
	}

	var tlsConfig *tlsProxyConfig
	if optsGetter("tls") == "true" {
		tlsConfig = &tlsProxyConfig{
			tlsInsecure:           optsGetter("tlsInsecure") == "true",
			serverName:            connStr.Hostname(),
			tlsCAFile:             optsGetter("tlsCAFile"),
			tlsCertificateKeyFile: optsGetter("tlsCertificateKeyFile"),
		}
	}

	return &proxy{
		ctx:              cancelCtx,
		host:             connStr.Hostname(),
		port:             connStr.Port(),
		username:         connStr.User.Username(),
		password:         passwd,
		serverRW:         serverRW,
		clientW:          clientW,
		clientInitBuffer: newBlockingReader(),
		tlsProxyConfig:   tlsConfig,
		withSrv:          strings.Contains(connStr.Scheme, "+srv"),
		authSource:       authSource,
		cancelFn:         cancelFn,
		connectionID:     fmt.Sprintf("%v", ctx.Value(ConnIDContextKey)),
		sid:              fmt.Sprintf("%v", ctx.Value(SIDContextKey)),
	}
}

func (p *proxy) initalizeConnection() error {
	if p.username == "" || p.password == "" {
		return fmt.Errorf("missing password or username")
	}
	if p.withSrv {
		return fmt.Errorf("mongodb+srv connection string is not supported")
	}
	log.Infof("initializing mongo session, user=%v, authSource=%v, sslmode=%v", p.username, p.authSource, p.tlsProxyConfig != nil)
	pkt, err := p.processPacket(p.clientInitBuffer)
	if err != nil {
		return fmt.Errorf("failed reading initial packet from client, err=%v", err)
	}

	tlsConn, err := p.tlsClientHandshake()
	if err != nil {
		return err
	}
	// upgrade connection to tls
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
	}
	return nil
}

func (p *proxy) processPacket(data io.Reader) (pkt *mongotypes.Packet, err error) {
	pkt, err = mongotypes.Decode(data)
	// the connection was closed, ignore any errors
	if p.closed || err == io.EOF {
		return nil, errConnectionClose
	}
	return pkt, err
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
