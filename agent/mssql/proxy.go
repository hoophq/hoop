package mssql

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/url"
	"time"

	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/common/mssql/types"
)

const (
	encryptOff    = 0 // Encryption is available but off.
	encryptOn     = 1 // Encryption is available and on.
	encryptNotSup = 2 // Encryption is not available.
	encryptReq    = 3 // Encryption is required.
)

type onRunErrFnType func(errMsg string)

type Proxy interface {
	Run(onErrCallback onRunErrFnType) Proxy
	Write(b []byte) (int, error)
	Done() <-chan struct{}
	Close() error
}

type proxy struct {
	ctx              context.Context
	tlsConfig        *tls.Config
	username         string
	password         string
	serverRW         io.ReadWriteCloser
	clientW          io.Writer
	clientInitBuffer io.ReadWriter
	cancelFn         context.CancelFunc
	packetSize       uint32
	initialized      bool
}

func NewProxy(ctx context.Context, connStr *url.URL, serverRW io.ReadWriteCloser, clientW io.Writer) Proxy {
	if connStr == nil {
		connStr = &url.URL{}
	}
	cancelCtx, cancelFn := context.WithCancel(ctx)
	passwd, _ := connStr.User.Password()
	return &proxy{
		ctx: cancelCtx,
		tlsConfig: &tls.Config{
			ServerName:         connStr.Hostname(),
			InsecureSkipVerify: connStr.Query().Get("insecure") == "true",
			// Go implementation of TLS payload size heuristic algorithm splits single TDS package to multiple TCP segments,
			// while SQL Server seems to expect one TCP segment per encrypted TDS package.
			// Setting DynamicRecordSizingDisabled to true disables that algorithm and uses 16384 bytes per TLS package
			DynamicRecordSizingDisabled: true,
		},
		username:         connStr.User.Username(),
		password:         passwd,
		serverRW:         serverRW,
		clientW:          clientW,
		clientInitBuffer: newBlockingReader(),
		cancelFn:         cancelFn}
}

func (r *proxy) readNextInitPacket(reader io.Reader, packetType types.PacketType) (pkt *types.Packet, err error) {
	pkt, err = types.Decode(reader)
	if err != nil {
		return
	}
	if pkt.Type() != packetType {
		return nil, fmt.Errorf("unknown packet, expected=[%X], found=[%X]", packetType, pkt.Type())
	}
	return
}

func (r *proxy) initalizeConnection() error {
	if r.username == "" || r.password == "" {
		return fmt.Errorf("missing password or username")
	}
	log.Infof("reading PRE-LOGIN")
	pkt, err := r.readNextInitPacket(r.clientInitBuffer, types.PacketPreloginType)
	if err != nil {
		return err
	}
	// Mutate the encryption option sent by the client
	// to negotiate packets with the server with with encryption.
	encryptOption := pkt.Frame[5:10]
	offset := binary.BigEndian.Uint16(encryptOption[1:3])
	pkt.Frame[offset] = encryptOn
	_, _ = r.serverRW.Write(pkt.Encode())

	log.Infof("reading PRE-LOGIN REPLY packet from server")
	pkt, err = r.readNextInitPacket(r.serverRW, types.PacketReplyType)
	if err != nil {
		return err
	}

	// Mutate the encryption option response sent by the server
	// informing that encryption is not supported.
	// This allows this process to offload TLS.
	encryptOption = pkt.Frame[5:10]
	offset = binary.BigEndian.Uint16(encryptOption[1:3])
	pkt.Frame[offset] = encryptNotSup
	_, _ = r.clientW.Write(pkt.Encode())

	log.Infof("reading LOGIN packet from client")
	pkt, err = r.readNextInitPacket(r.clientInitBuffer, types.PacketLogin7Type)
	if err != nil {
		return err
	}

	l := types.DecodeLogin(pkt.Frame)
	l.UserName = r.username
	l.Password = r.password
	l.ServerName = r.tlsConfig.ServerName
	l.DisablePasswordChange()

	// TODO: we should validate the reply response when setting this option,
	// otherwise further requests may fail
	r.packetSize = l.PacketSize()
	log.Infof("decoded LOGIN packet from client. tds-version=%X, app-name=%v, database=%v, hostname=%v, servername=%v, packet-size=%v",
		l.TDSVersion(), l.AppName, l.Database, l.HostName, l.ServerName, l.PacketSize())

	pkt, err = types.EncodeLogin(*l)
	if err != nil {
		return fmt.Errorf("failed encoding login: %v", err)
	}

	conn, ok := r.serverRW.(net.Conn)
	if !ok {
		return fmt.Errorf("server is not a net.Conn type")
	}
	conn.SetDeadline(time.Now().Add(time.Second * 15))

	log.Infof("begin tls handshake")
	handshakeConn := &tlsHandshakeConn{c: conn}
	tlsConn := tls.Client(handshakeConn, r.tlsConfig)
	err = tlsConn.Handshake()
	if err != nil {
		if verr, ok := err.(tls.RecordHeaderError); ok {
			return fmt.Errorf("tls handshake error=%v, message=%v, record-header=%X",
				verr.Msg, verr.Error(), verr.RecordHeader[:])
		}
		return fmt.Errorf("tls handshake error=%v", err)
	}
	handshakeConn.upgraded = true
	log.Infof("connection upgraded with tls")
	r.serverRW = tlsConn
	_, _ = r.serverRW.Write(pkt.Encode())
	return nil
}

func (r *proxy) processPacket(data io.Reader) (*types.Packet, error) { return types.Decode(data) }

// Run reads packets in a goroutine of the server and writes back to client
func (r *proxy) Run(onErrCallback onRunErrFnType) Proxy {
	if onErrCallback == nil {
		onErrCallback = func(errMsg string) {} // noop callback
	}
	initCh := make(chan error)
	go func() { initCh <- r.initalizeConnection() }()
	go func() {
		if err := <-initCh; err != nil {
			errMsg := fmt.Sprintf("failed initializing connection, reason=%v", err)
			log.Warn(errMsg)
			r.Close()
			close(initCh)
			onErrCallback(errMsg)
			return
		}
		r.initialized = true
		log.Infof("initialized tds session with success")
		defer r.Close()
	exit:
		for {
			select {
			case <-r.Done():
				log.Infof("context done, err=%v", r.ctx.Err())
				break exit
			default:
				pkt, err := r.processPacket(r.serverRW)
				if err != nil {
					if err != io.EOF {
						errMsg := fmt.Sprintf("failed processing packet, reason=%v", err)
						log.Warn(errMsg)
						onErrCallback(errMsg)
					}
					break exit
				}
				if pkt != nil {
					if _, err := r.clientW.Write(pkt.Encode()); err != nil {
						errMsg := fmt.Sprintf("failed writing packet to stream, reason=%v", err)
						log.Warn(errMsg)
						onErrCallback(errMsg)
						break exit
					}
				}
			}
		}
		log.Infof("done reading")
	}()
	return r
}

func (r *proxy) Done() <-chan struct{} { return r.ctx.Done() }
func (r *proxy) Close() error {
	r.serverRW.Close()
	r.cancelFn()
	return nil
}

func (r *proxy) Write(b []byte) (n int, err error) {
	if !r.initialized {
		return r.clientInitBuffer.Write(b)
	}
	pkt, err := r.processPacket(bytes.NewBuffer(b))
	if err != nil || pkt == nil {
		return
	}
	return r.serverRW.Write(pkt.Encode())
}
