package proxy

import (
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	sshtypes "libhoop/proxy/ssh/types"

	charmlog "github.com/charmbracelet/log"
	"github.com/creack/pty"

	// "github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
)

var clog = charmlog.New(os.Stderr)

// from syscall.SIGWINCH, avoid syscall errors when compiling on Windows
const SIGWINCH = syscall.Signal(0x1c)

type SSHServer struct {
	listenHost      Host
	client          pb.ClientTransport
	serverConfig    *ssh.ServerConfig
	connectionStore memory.Store
	listener        net.Listener
	packetType      pb.PacketType
	hostKey         ssh.Signer
	oldState        *term.State
	sshClientExec   *exec.Cmd
	isDebug         bool
	isSshClientExec bool
}

func NewSSHServer(listenPort, connName string, client pb.ClientTransport, hostKey ssh.Signer, isDebug bool) (*SSHServer, error) {
	logLevel := charmlog.InfoLevel
	if isDebug {
		logLevel = charmlog.DebugLevel
	}
	clog = charmlog.NewWithOptions(os.Stderr, charmlog.Options{
		Level:  logLevel,
		Prefix: fmt.Sprintf("%s 🔒", connName),
	})
	listenHost := getListenAddr(defaultListenAddr(defaultSSHPort))
	if listenPort == "" {
		var err error
		listenHost, err = getAvailableLocalAddress(listenHost.Host, listenHost.Port)
		if err != nil {
			return nil, fmt.Errorf("failed obtaining any local address available, reason=%v", err)
		}
	}

	config := &ssh.ServerConfig{
		NoClientAuth: true, // Ignore client authentication
		// PasswordCallback: func(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		// 	// Ignore client credentials and always allow
		// 	return &ssh.Permissions{}, nil
		// },
	}
	return &SSHServer{
		listenHost:      listenHost,
		client:          client,
		serverConfig:    config,
		connectionStore: memory.New(),
		hostKey:         hostKey,
		packetType:      pbagent.SSHConnectionWrite,
		isDebug:         isDebug,
		isSshClientExec: listenPort == "",
	}, nil
}

func (p *SSHServer) Serve(sid string) error {
	if p.hostKey == nil {
		randomHostKey, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return fmt.Errorf("failed to generate host key: %v", err)
		}
		randomHostKeySigner, err := ssh.NewSignerFromKey(randomHostKey)
		if err != nil {
			return fmt.Errorf("failed to signer host key: %v", err)
		}
		p.hostKey = randomHostKeySigner
	}

	// It requires at least one key to host an SSH server.
	// Generate a random key just to satisfy this requirement.
	// In practice, the client will establish a plain SSH connection
	// to localhost TLS secured by the gRPC gateway.
	p.serverConfig.AddHostKey(p.hostKey)

	lis, err := net.Listen("tcp4", p.listenHost.Addr())
	if err != nil {
		return fmt.Errorf("failed listening to address %v, err=%v", p.listenHost.Addr(), err)
	}
	p.listener = lis
	go func() {
		connectionID := 0
		for {
			connectionID++
			conn, err := lis.Accept()
			if err != nil {
				clog.Debugf("failed obtain listening connection, err=%v", err)
				lis.Close()
				break
			}
			go p.serveConn(sid, strconv.Itoa(connectionID), conn)
		}
	}()

	clog.Infof("proxy started, ready to accept connections at %s", p.listenHost.Addr())
	if !p.isSshClientExec {
		clog.Infof("use the ssh client command below to connect in another terminal")
		clog.Info(fmt.Sprintf("ssh %s -p %s -o StrictHostKeyChecking=no", p.listenHost.Host, p.listenHost.Port))
	}
	return nil
}

func (p *SSHServer) serveConn(sid, connectionID string, conn net.Conn) {
	defer func() {
		clog.Debug("closing ssh connection", "sid", sid, "conn", connectionID, "remote", conn.RemoteAddr())
		p.connectionStore.Del(connectionID)
		_ = conn.Close()
		_ = p.client.Send(&pb.Packet{
			Type: pbagent.TCPConnectionClose,
			Spec: map[string][]byte{
				pb.SpecClientConnectionID: []byte(connectionID),
				pb.SpecGatewaySessionID:   []byte(sid),
			}})
	}()

	p.connectionStore.Set(connectionID, conn)
	sshConn, clientNewCh, sshReq, err := ssh.NewServerConn(conn, p.serverConfig)
	if err != nil {
		clog.Debugf("session=%v | conn=%v - failed to establish handshake with client: %v", sid, connectionID, err)
		conn.Close()
		return
	}
	go ssh.DiscardRequests(sshReq)
	defer sshConn.Close()

	clog.Debug("ssh connection established", "sid", sid, "conn", connectionID, "remote", conn.RemoteAddr())
	spec := map[string][]byte{
		string(pb.SpecGatewaySessionID):   []byte(sid),
		string(pb.SpecClientConnectionID): []byte(connectionID),
	}
	streamWriter := pb.NewStreamWriter(p.client, p.packetType, spec)
	channelID := uint16(0)
	for newCh := range clientNewCh {
		channelID++
		go p.handleChannel(newCh, streamWriter, connectionID, channelID)
	}
}

func (p *SSHServer) ServeAndConnect(sid string) error {
	info, err := os.Stdin.Stat()
	if err != nil {
		return fmt.Errorf("failed obtaining stdin file description, err=%v", err)
	}
	if info.Mode()&os.ModeCharDevice == 0 || info.Size() > 0 {
		return fmt.Errorf("could not allocate a tty, wrong type of device")
	}

	// starting listening to SSH connections on the specified port
	if err := p.Serve(sid); err != nil {
		return err
	}

	// establishing a connection with the SSH server
	return p.runSSHClient()
}

func (p *SSHServer) runSSHClient() error {
	// TODO: check if ssh command exists
	p.sshClientExec = exec.Command("ssh", p.listenHost.Host, "-p", p.listenHost.Port, "-o", "StrictHostKeyChecking=no")

	clog.Info(p.sshClientExec.String())
	fmt.Fprintln(os.Stderr, "")

	ptmx, err := pty.Start(p.sshClientExec)
	if err != nil {
		return fmt.Errorf("failed to start pty: %v", err)
	}
	// ensure restoring term in case of command failure
	defer restoreTerm(p.oldState)

	// Handle pty size.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, SIGWINCH)
	go func() {
		for range sig {
			if err := pty.InheritSize(os.Stdin, ptmx); err != nil {
				clog.Warnf("error resizing pty: %s", err)
			}
		}
	}()
	sig <- SIGWINCH // Initial resize.

	// Copy stdin to the pty and the pty to stdout.
	// NOTE: The goroutine will keep reading until the next keystroke before returning.
	go func() {
		if _, err = io.Copy(ptmx, os.Stdin); err != nil {
			clog.Warnf("error copying stdin to pty: %s", err)
		}
	}()

	go func() {
		if _, err = io.Copy(os.Stdout, ptmx); err != nil {
			clog.Warnf("error copying pty to stdout: %s", err)
		}
	}()

	go func() {
		<-p.client.StreamContext().Done()
		signal.Stop(sig)
		_ = ptmx.Close()
		p.Close()
	}()
	return nil
}

func (p *SSHServer) handleChannel(newCh ssh.NewChannel, streamW io.Writer, connID string, channelID uint16) {
	// log.With("ch", channelID, "conn", connID).Infof("received new channel, type=%v", newCh.ChannelType())
	chType, chExtra := newCh.ChannelType(), newCh.ExtraData()
	clientCh, clientRequests, err := newCh.Accept()
	if err != nil {
		clog.With("ch", channelID, "conn", connID).Debugf("failed obtaining channel, err=%v", err)
		return
	}

	p.connectionStore.Set(fmt.Sprintf("%s:%v", connID, channelID), clientCh)
	openChData := (sshtypes.OpenChannel{
		ChannelID:        channelID,
		ChannelType:      chType,
		ChannelExtraData: chExtra,
	}).Encode()
	if _, err := streamW.Write([]byte(openChData)); err != nil {
		clog.With("ch", channelID, "conn", connID).Debugf("unable to write open channel to stream, err=%v", err)
		return
	}

	go func() {
		defer clientCh.Close()
		_, err = io.Copy(sshtypes.NewDataWriter(streamW, channelID), clientCh)
		clog.With("ch", channelID, "conn", connID).Debugf("done copying ssh buffer, err=%v", err)
	}()

	go func() {
		for req := range clientRequests {
			data := (sshtypes.SSHRequest{
				ChannelID:   channelID,
				RequestType: req.Type,
				WantReply:   req.WantReply,
				Payload:     req.Payload,
			}).Encode()
			clog.With("ch", channelID, "conn", connID, "type", req.Type).Debug("received client ssh request")
			_, err := streamW.Write([]byte(data))
			if err != nil {
				clog.With("ch", channelID, "conn", connID).Debugf("failed writing to stream, err=%v", err)
				return
			}
			if req.WantReply {
				if err := req.Reply(true, nil); err != nil {
					clog.With("ch", channelID, "conn", connID).Debugf("failed sending response to channel, err=%v", err)
					return
				}
			}
		}
		clog.With("ch", channelID, "conn", connID).Debugf("done processing ssh client requests")
	}()
}

func (p *SSHServer) PacketWriteClient(connectionID string, pkt *pb.Packet) (int, error) {
	if p.isSshClientExec && p.oldState == nil {
		// Set stdin in raw mode when the first packet is received
		// it avoids breaking any logging that happens before the first packet
		// is received, e.g. the local SSH client connection.
		var err error
		p.oldState, err = term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			return 0, err
		}
	}

	switch sshtypes.DecodeType(pkt.Payload) {
	case sshtypes.DataType:
		var data sshtypes.Data
		if err := sshtypes.Decode(pkt.Payload, &data); err != nil {
			return 0, err
		}
		connWrapperObj := p.connectionStore.Get(fmt.Sprintf("%s:%v", connectionID, data.ChannelID))
		clientCh, ok := connWrapperObj.(io.WriteCloser)
		if !ok {
			return 0, fmt.Errorf("local channel %q not found", connectionID)
		}
		return clientCh.Write(data.Payload)
	case sshtypes.CloseChannelType:
		var cc sshtypes.CloseChannel
		if err := sshtypes.Decode(pkt.Payload, &cc); err != nil {
			return 0, err
		}

		if isLocalCh := cc.ID == 1 && p.isSshClientExec; isLocalCh {
			_ = p.Close()
			// break line to separate the output
			fmt.Fprintln(os.Stderr, "")
			clog.Infof("ssh client disconnected")
		}

		obj := p.connectionStore.Get(fmt.Sprintf("%s:%v", connectionID, cc.ID))
		if clientCh, ok := obj.(io.Closer); ok {
			err := clientCh.Close()
			log.With("ch", cc.ID, "conn", connectionID).Debugf("closing client ssh channel type=%v, err=%v",
				cc.Type, err)
		}
		return 0, nil
	default:
		return 0, fmt.Errorf("unknown ssh message type (%v)", sshtypes.DecodeType(pkt.Payload))
	}
}

func (p *SSHServer) CloseTCPConnection(connectionID string) {
	if conn, err := p.getConnection(connectionID); err == nil {
		_ = conn.Close()
	}
}

func (p *SSHServer) Close() error {
	if p.oldState != nil {
		restoreTerm(p.oldState)
		p.oldState = nil
	}
	if p.sshClientExec != nil {
		p.sshClientExec.Process.Signal(syscall.SIGINT) // Send interrupt signal to the SSH client
	}
	_ = p.listener.Close()
	_, _ = p.client.Close()
	return nil
}

func (p *SSHServer) getConnection(connectionID string) (io.WriteCloser, error) {
	connWrapperObj := p.connectionStore.Get(connectionID)
	conn, ok := connWrapperObj.(io.WriteCloser)
	if !ok {
		return nil, fmt.Errorf("local connection %q not found", connectionID)
	}
	return conn, nil
}

func (p *SSHServer) Host() Host { return p.listenHost }

func isAddressAvailable(addr string) bool {
	timeout := time.Second * 5
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil && strings.Contains(err.Error(), "connect: connection refused") {
		return true
	}
	_ = conn.Close()
	return false
}

func getAvailableLocalAddress(sshHost, sshPort string) (host Host, err error) {
	sshPortInt, _ := strconv.Atoi(sshPort)
	for range 10 {
		sshPortInt++
		addr := fmt.Sprintf("%s:%d", sshHost, sshPortInt)
		isAvailable := isAddressAvailable(addr)
		clog.Debug("checking address local availability", "addr", addr, "available", isAvailable)
		if isAvailable {
			host = Host{
				Host: sshHost,
				Port: strconv.Itoa(sshPortInt),
			}
			break
		}
	}
	if host.Host == "" {
		return Host{}, fmt.Errorf("failed to find an available port (%v to %v) for SSH server", sshPort, sshPortInt)
	}
	return host, nil
}
