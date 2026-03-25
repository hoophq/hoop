package rdp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"sync"
	"time"

	"github.com/hoophq/hoop/gateway/idp"
	"github.com/hoophq/hoop/gateway/proxyproto/tlstermination"
	"github.com/hoophq/hoop/gateway/transport"

	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/broker"
	"github.com/hoophq/hoop/gateway/models"
)

var (
	store       = sync.Map{}
	instanceKey = "rdp_instance"
)

type RDPProxy struct {
	listener   net.Listener
	ctx        context.Context
	listenAddr string
}

// GetServerInstance returns the singleton instance of PGServer.
func GetServerInstance() *RDPProxy {
	server, _ := store.LoadOrStore(instanceKey, &RDPProxy{})
	return server.(*RDPProxy)
}

func (r *RDPProxy) Start(listenAddr string, tlsConfig *tls.Config, acceptPlainText bool) error {
	if _, ok := store.Load(instanceKey); ok && r.listener != nil {
		return nil
	}

	log.Infof("starting rdp server proxy at %v", listenAddr)
	//start new tcp listener for rdp clients
	server, err := runRDPProxyServer(listenAddr, tlsConfig, acceptPlainText)
	if err != nil {
		return err
	}
	store.Store(instanceKey, server)
	return nil
}

func (r *RDPProxy) Stop() error {
	if serverAny, ok := store.LoadAndDelete(instanceKey); ok {
		server, ok := serverAny.(*RDPProxy)
		if !ok {
			log.Errorf("invalid server type %v", reflect.TypeOf(serverAny))
			return errors.New("invalid server type")
		}
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

	// if tlsConfig != nil {
	// 	listener = tlstermination.NewTLSTermination(listener, tlsConfig, acceptPlainText)
	// }

	rdpProxyInstance := &RDPProxy{
		listener:   listener,
		listenAddr: listenAddr,
	}

	go func() {
		defer listener.Close()
		for {
			conn, err := listener.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					log.Info("proxy server listener closed, stopping accepting new connections")
					return
				}
				log.Errorf("RDP accept error: %v", err)
				if conn != nil {
					_ = conn.Close()
				}
				continue
			}

			go rdpProxyInstance.handleRDPClient(conn, conn.RemoteAddr())
		}
	}()

	return rdpProxyInstance, nil
}

func buildGenericRdpErrorPacket() []byte {
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

	return append(tpkt, userData...)
}

// sendGenericRdpError tells the RDP client "something went wrong".
// Most clients (mstsc, FreeRDP) will show a generic protocol/security error dialog.
func sendGenericRdpError(conn net.Conn) error {
	packet := buildGenericRdpErrorPacket()
	// Write the failure once, then close
	if _, err := conn.Write(packet); err != nil {
		return err
	}
	return conn.Close()
}

func (r *RDPProxy) handleRDPClient(conn net.Conn, peerAddr net.Addr) {
	defer conn.Close()
	connection := broker.NewClientCommunicator(conn)

	var firstRDPData []byte
	var err error

	if metaConn, ok := conn.(*tlstermination.TLSConnectionMeta); ok {
		firstRDPData = metaConn.RDPCookie
	} else {
		// Read the first RDP packet
		firstRDPData, err = ReadFirstRDPPacket(conn)
		if err != nil {
			// Prevents log pollution from health check requests on this port
			if err == io.EOF {
				log.Debugf("failed to read first RDP packet, reason=EOF error")
				return
			}
			log.Warnf("Failed to read first RDP packet: %v", err)
			return
		}
	}

	ctxDuration, dba, connectionModel, tokenVerifier, extractedCreds, err := checkAndPrepareRDP(firstRDPData)
	if err != nil {
		log.Printf("Failed to check and prepare RDP: %v", err)
		_ = sendGenericRdpError(conn)
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

	transport.PollingUserToken(context.Background(), func(cause error) {
		session.Close()
	}, tokenVerifier, dba.UserSubject)

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

func checkAndPrepareRDP(firstRDPData []byte) (duration time.Duration, at *models.ConnectionCredentials, model *models.Connection, verifier idp.UserInfoTokenVerifier, creds string, err error) {
	// Extract credentials from headers
	extractedCreds, err := ExtractCredentialsFromRDP(firstRDPData)
	if err != nil {
		log.Errorf("Failed to extract credentials: %v", err)
		return duration, at, model, verifier, creds, err
	}

	secretKeyHash, err := keys.Hash256Key(extractedCreds)
	if err != nil {
		log.Errorf("failed hashing rdp secret key, reason=%v", err)
		return duration, at, model, verifier, creds, err
	}

	dba, err := models.GetValidConnectionCredentialsBySecretKey(
		[]string{pb.ConnectionTypeRDP.String()},
		secretKeyHash)

	if err != nil {
		if errors.Is(err, models.ErrNotFound) {
			// it is possible to use just mapped errors for client responses
			log.Errorf("invalid credentials provided by rdp client, reason=%v", err)
			return duration, at, model, verifier, creds, err
		}
		log.Errorf("failed obtaining secret access key, reason=%v", err)
		return duration, at, model, verifier, creds, err
	}

	ctxDuration := dba.ExpireAt.Sub(time.Now().UTC())
	if ctxDuration <= 0 {
		log.Errorf("invalid secret access key credentials")
		return duration, at, model, verifier, creds, fmt.Errorf("expired credentials")
	}

	tokenVerifier, _, err := idp.NewUserInfoTokenVerifierProvider()
	if err != nil {
		log.Errorf("failed to load IDP provider: %v", err)
		return duration, at, model, verifier, creds, err
	}

	if err := transport.CheckUserToken(tokenVerifier, dba.UserSubject); err != nil {
		log.Errorf("Error verifying the user token: %v", err)
		return duration, at, model, verifier, creds, err
	}

	userCtx, err := models.GetUserContext(dba.UserSubject)
	if err != nil {
		log.Errorf("failed fetching user context, reason=%v", err)
		return duration, at, model, verifier, creds, err
	}

	connectionModel, err := models.GetConnectionByNameOrID(userCtx, dba.ConnectionName)
	if connectionModel == nil || err != nil {
		log.Errorf("failed fetching connection by name or id, reason=%v", err)
		return duration, at, model, verifier, creds, err
	}

	return ctxDuration, dba, connectionModel, tokenVerifier, extractedCreds, nil
}
