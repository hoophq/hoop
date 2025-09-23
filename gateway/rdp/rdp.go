package rdp

import (
	"net"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	"github.com/hoophq/hoop/gateway/broker"
)

var (
	store       = memory.New() // TODO change memory for sync.Map
	instanceKey = "rdp_instance"
)

type RDPSessionInfo struct {
	SessionID           uuid.UUID
	Username            string
	TargetAddress       string
	OrgID               string
	Password            string
	DataChannel         chan []byte
	CredentialsReceived chan bool
}

type RDPProxy struct {
	connectionStore memory.Store
	listener        net.Listener
	listenAddr      string
	sessions        map[uuid.UUID]*RDPSessionInfo
}

// GetServerInstance returns the singleton instance of PGServer.
func GetServerInstance() *RDPProxy {
	if server, ok := store.Get(instanceKey).(*RDPProxy); ok {
		return server
	}
	server := &RDPProxy{}
	store.Set(instanceKey, server)
	return server
}

func (r *RDPProxy) Start(listenAddr string) error {
	if _, ok := store.Get(instanceKey).(*RDPProxy); ok && r.listener != nil {
		return nil
	}

	log.Infof("starting rdp server proxy at %v", listenAddr)
	//start new tcp listener for rdp clients
	server, err := runRDPProxyServer(listenAddr)
	if err != nil {
		return err
	}
	store.Set(instanceKey, server)
	return nil
}

func (r *RDPProxy) Stop() error {
	if server, ok := store.Pop(instanceKey).(*RDPProxy); ok {
		if r.connectionStore == nil {
			return nil
		}

		if server.listener != nil {
			log.Infof("stopping postgres server proxy at %v", server.listener.Addr().String())
			_ = server.listener.Close()
		}
	}

	return nil
}

func runRDPProxyServer(listenAddr string) (*RDPProxy, error) {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, err
	}

	proxy := &RDPProxy{
		connectionStore: memory.New(),
		listener:        listener,
		listenAddr:      listenAddr,
	}

	go func() {
		defer listener.Close()
		for {
			conn, err := listener.Accept()
			if err != nil {
				log.Errorf("RDP accept error: %v", err)
				continue
			}

			go proxy.handleRDPClient(conn, conn.RemoteAddr())
		}
	}()

	return proxy, nil
}

func (r *RDPProxy) handleRDPClient(conn net.Conn, peerAddr net.Addr) {
	defer conn.Close()

	log.Printf("TCP connection from %s", peerAddr)

	// Generate session ID
	connection := &broker.Connection{
		ID:         "connection-id",
		ConnType:   "tcp",
		Connection: conn,
	}

	// Read first RDP packet
	firstRDPData, err := ReadFirstRDPPacket(conn)
	if err != nil {
		log.Printf("Failed to read first RDP packet: %v", err)
		return
	}

	// Extract credentials
	extractedCreds, err := ExtractCredentialsFromRDP(firstRDPData)
	if err != nil {
		log.Printf("Failed to extract credentials: %v", err)
		return
	}

	log.Printf("Extracted credentials: %s", extractedCreds)

	// extract agent from the db for this connection keys
	session, err := broker.CreateSession(
		connection,
		broker.MockConnection(),
		peerAddr.String())

	if err != nil {
		log.Printf("Failed to create session: %v", err)
		return
	}

	if session == nil {
		log.Printf("CreateSession returned nil session")
		return
	}

	// Register session
	// Clean up session on exit
	defer func() {
		if session != nil {
			session.Close()
		}
	}()

	// Start data forwarding
	go session.StartingForwardind(firstRDPData)
	session.SendAgentToTCP()

}
