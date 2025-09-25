package rdp

import (
	"context"
	"fmt"
	"net"

	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/broker"
	"github.com/hoophq/hoop/gateway/models"
)

var (
	store       = memory.New() // TODO change memory for sync.Map
	instanceKey = "rdp_instance"
)

type RDPProxy struct {
	listener   net.Listener
	ctx        context.Context
	listenAddr string
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

		for _, session := range broker.GetSessions() {
			if session != nil {
				log.Infof("closing session %v", session.ID)
				session.Close()
			}
		}
		if server.listener != nil {
			log.Infof("stopping rdp server proxy at %v", server.listener.Addr().String())
			_ = server.listener.Close()
		}
	}

	return nil
}

func runRDPProxyServer(listenAddr string) (*RDPProxy, error) {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to start RDP proxy server at %v, reason=%v", listenAddr, err)
	}

	proxy := &RDPProxy{
		listener:   listener,
		listenAddr: listenAddr,
	}

	go func() {
		defer listener.Close()
		for {
			conn, err := listener.Accept()
			if err != nil {
				log.Errorf("RDP accept error: %v", err)
				break
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

	secretKeyHash, err := keys.Hash256Key(extractedCreds)
	log.Printf("Extracted credentials: %s", extractedCreds)
	dba, err := models.GetValidConnectionCredentialsBySecretKey(
		pb.ConnectionTypeRDP.String(),
		secretKeyHash)
	if err != nil {
		if err == models.ErrNotFound {
			log.Errorf("invalid secret access key credentials")
			return
		}
		log.Errorf("failed obtaining secret access key, reason=%v", err)
		return
	}
	fmt.Println("obtained db access by id, id=", dba.ID, ", subject=", dba.UserSubject, ", connection=", dba.ConnectionName)

	userCtx, err := models.GetUserContext(dba.UserSubject)
	if err != nil {
		log.Errorf("failed fetching user context, reason=%v", err)
		return
	}
	connectionModel, err := models.GetConnectionByNameOrID(userCtx, dba.ConnectionName)
	if err != nil {
		log.Errorf("failed fetching connection by name or id, reason=%v", err)
		return
	}

	session, err := broker.CreateRDPSession(
		connection,
		*connectionModel,
		extractedCreds,
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
