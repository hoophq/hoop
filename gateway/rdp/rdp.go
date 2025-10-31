package rdp

import (
	"context"
	"crypto/tls"
	"fmt"
	"github.com/hoophq/hoop/gateway/proxyproto/tlstermination"
	"net"
	"time"

	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/memory"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/broker"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
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

func (r *RDPProxy) Start(listenAddr string, tlsConfig *tls.Config, acceptPlainText bool) error {
	if _, ok := store.Get(instanceKey).(*RDPProxy); ok && r.listener != nil {
		return nil
	}

	log.Infof("starting rdp server proxy at %v", listenAddr)
	//start new tcp listener for rdp clients
	server, err := runRDPProxyServer(listenAddr, tlsConfig, acceptPlainText)
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

func runRDPProxyServer(listenAddr string, tlsConfig *tls.Config, acceptPlainText bool) (*RDPProxy, error) {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to start RDP proxy server at %v, reason=%v", listenAddr, err)
	}

	if tlsConfig != nil {
		listener = tlstermination.NewTLSTermination(listener, tlsConfig, acceptPlainText)
	}
	
	rdpProxyInstance := &RDPProxy{
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

			go rdpProxyInstance.handleRDPClient(conn, conn.RemoteAddr())
		}
	}()

	return rdpProxyInstance, nil
}

// sendGenericRdpError tells the RDP client "something went wrong".
// Most clients (mstsc, FreeRDP) will show a generic protocol/security error dialog.
func sendGenericRdpError(conn net.Conn) error {
	// It depends on the client to show specific error messages.
	// X.224 Connection Confirm header
	x224 := []byte{
		0x0e,       // length of header
		0xd0,       // type=CC
		0x00, 0x00, // dstRef
		0x12, 0x34, // srcRef (arbitrary)
		0x00, // class/options
	}

	// RDP_NEG_FAILURE { type=0x03, flags=0, length=8, failureCode=0x00000002 }
	// 0x00000002 => SSL_NOT_ALLOWED_BY_SERVER (common generic error)
	neg := []byte{
		0x03, 0x00, 0x08, 0x00,
		0x02, 0x00, 0x00, 0x00,
	}

	userData := append(x224, neg...)

	// TPKT header: version=3, reserved=0, total length
	totalLen := 4 + len(userData)
	tpkt := []byte{
		0x03, 0x00,
		byte(totalLen >> 8), byte(totalLen & 0xff),
	}

	packet := append(tpkt, userData...)

	// Write the failure once, then close
	if _, err := conn.Write(packet); err != nil {
		return err
	}
	return conn.Close()
}

func (r *RDPProxy) handleRDPClient(conn net.Conn, peerAddr net.Addr) {
	defer conn.Close()

	connection := broker.NewClientCommunicator(conn)

	// Read first RDP packet
	firstRDPData, err := ReadFirstRDPPacket(conn)
	if err != nil {
		log.Errorf("Failed to read first RDP packet: %v", err)
		return
	}

	// Extract credentials from headers
	extractedCreds, err := ExtractCredentialsFromRDP(firstRDPData)
	if err != nil {
		log.Errorf("Failed to extract credentials: %v", err)
		return
	}

	secretKeyHash, err := keys.Hash256Key(extractedCreds)

	dba, err := models.GetValidConnectionCredentialsBySecretKey(
		pb.ConnectionTypeRDP.String(),
		secretKeyHash)

	if err != nil {
		if err == models.ErrNotFound {
			// it is possible use just mapped errors for client responses
			log.Errorf("invalid credentials provided by rdp client, reason=%v", err)
			_ = sendGenericRdpError(conn)
			return
		}
		log.Errorf("failed obtaining secret access key, reason=%v", err)
		return
	}

	ctxDuration := dba.ExpireAt.Sub(time.Now().UTC())
	if ctxDuration <= 0 {
		log.Errorf("invalid secret access key credentials")
		return
	}

	connectionModel, err := models.GetConnectionByNameOrID(storagev2.NewOrganizationContext(dba.OrgID), dba.ConnectionName)
	if err != nil {
		log.Errorf("failed fetching connection by name or id, reason=%v", err)
		return
	}

	session, err := broker.CreateRDPSession(
		connection,
		*connectionModel,
		peerAddr.String(),
		broker.ProtocolRDP,
		extractedCreds,
		dba.ExpireAt,
		ctxDuration,
	)

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
	go session.ForwardToAgent(firstRDPData)
	session.ForwardToClient()

}
