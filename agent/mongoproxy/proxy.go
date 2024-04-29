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
}

func New(ctx context.Context, connStr *url.URL, serverRW io.ReadWriteCloser, clientW io.ReadWriter) *proxy {
	if connStr == nil {
		connStr = &url.URL{}
	}

	cancelCtx, cancelFn := context.WithCancel(ctx)
	passwd, _ := connStr.User.Password()
	optsGetter := connStr.Query().Get

	// use authSource, or the database if is provided, otherwiser fallback to default db
	// https://github.com/mongodb/specifications/blob/master/source/auth/auth.md#connection-string-options
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
	}
}

func (p *proxy) initalizeConnection() error {
	if p.username == "" || p.password == "" {
		return fmt.Errorf("missing password or username")
	}
	if p.withSrv {
		return fmt.Errorf("mongodb+srv connection string is not supported")
	}

	log.With("conn", p.connectionID).Infof("initializing connection, host=%v, auth-source=%v, tls=%v",
		p.host, p.authSource, p.tlsProxyConfig != nil)
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

	err = p.handleServerAuth(pkt)
	// Authentication must not happen on monitoring only sockets.
	// Make sure to bypass these packets/
	// https://github.com/mongodb/specifications/blob/master/source/auth/auth.md#authentication
	if err == errNonSpeculativeAuthConnection {
		log.With("conn", p.connectionID).Debug("monitoring only connection packet")
		if _, err := p.serverRW.Write(pkt.Encode()); err != nil {
			return fmt.Errorf("failed write non monitoring packet, err=%v", err)
		}
		return nil
	}
	log.With("conn", p.connectionID).Infof("initialized authenticated session, host=%v, tls=%v", p.host, tlsConn != nil)
	return err
}

func (p *proxy) processPacket(data io.Reader) (pkt *mongotypes.Packet, err error) {
	pkt, err = mongotypes.Decode(data)
	// the connection was closed, ignore any errors
	if p.closed || err == io.EOF {
		return nil, errConnectionClose
	}
	return pkt, err
}

// Run start the proxy by offloading the authentication and the tls with the mongo server
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
			// TODO: return mongo protocol errors to client!
			onErrCallback(errMsg)
			return
		}
		p.initialized = true
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
			}
		}
		log.With("conn", p.connectionID).Infof("end connection, host=%v", p.host)
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
		return p.clientInitBuffer.Write(b)
	}
	pkt, err := p.processPacket(bytes.NewBuffer(b))
	if err != nil || pkt == nil {
		return
	}
	return p.serverRW.Write(pkt.Encode())
}
