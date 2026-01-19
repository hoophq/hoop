package rdp

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/broker"
	"github.com/hoophq/hoop/gateway/idp"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/transport"
)

var (
	instanceKeyIron = "ironrdp_gateway_instance"
	upgrader        = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}
)

// GetIronServerInstance returns the singleton instance of Iron RDP Gateway Proxy.
func GetIronServerInstance() *IronRDPGateway {
	server, _ := store.LoadOrStore(instanceKeyIron, &IronRDPGateway{})
	return server.(*IronRDPGateway)
}

type IronRDPGateway struct {
	connections atomic.Int32
}

func (r *IronRDPGateway) AttachHandlers(router gin.IRouter) {
	router.Handle(http.MethodGet, "/", r.handle)
	router.Handle(http.MethodPost, "/client", r.handleClient)
}

func (r *IronRDPGateway) handleClient(c *gin.Context) {
	rdpCredential := c.PostForm("credential")
	if rdpCredential == "" {
		log.Errorf("failed to get credential, reason=empty")
		c.String(http.StatusBadRequest, "Invalid request")
		return
	}
	secretKeyHash, err := keys.Hash256Key(rdpCredential)
	if err != nil {
		log.Errorf("failed hashing rdp secret key, reason=%v", err)
		c.String(http.StatusBadRequest, "Invalid request")
		return
	}

	dba, err := models.GetValidConnectionCredentialsBySecretKey([]string{pb.ConnectionTypeRDP.String()}, secretKeyHash)
	if err != nil {
		log.Errorf("failed to get connection by id, reason=%v", err)
		c.String(http.StatusBadRequest, "Invalid request")
		return
	}

	ctxDuration := dba.ExpireAt.Sub(time.Now().UTC())
	if ctxDuration <= 0 {
		log.Errorf("invalid secret access key credentials")
		c.String(http.StatusBadRequest, "Invalid request")
		return
	}

	tokenVerifier, _, err := idp.NewUserInfoTokenVerifierProvider()
	if err != nil {
		log.Errorf("failed to load IDP provider: %v", err)
		c.String(http.StatusBadRequest, "Invalid request")
		return
	}

	if err = transport.CheckUserToken(tokenVerifier, dba.UserSubject); err != nil {
		log.Errorf("Error verifying the user token: %v", err)
		c.String(http.StatusBadRequest, "Invalid request")
		return
	}

	// We don't need to do extended checks now because websocket will do it.

	data := renderWebClientTemplate("RDP Connection", rdpCredential)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(data))
}

func (r *IronRDPGateway) handle(c *gin.Context) {
	// Generate unique connection id
	connId := r.connections.Add(1)
	defer func() {
		r.connections.Add(-1)
	}()
	userAgent := c.GetHeader("User-Agent")

	cID := strconv.Itoa(int(connId))
	sessionID := uuid.NewString()
	peerAddr := c.Request.RemoteAddr

	log.With("sid", sessionID, "conn", cID).Infof("new websocket connection request for userAgent=%q",
		userAgent)

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.With("sid", sessionID, "conn", cID).
			Errorf("failed to upgrade websocket connection, reason=%v", err)
		c.String(http.StatusInternalServerError, "Failed to upgrade websocket")
		return
	}

	defer ws.Close()

	// Receive the first message
	_, msg, err := ws.ReadMessage()
	if err != nil {
		log.With("sid", sessionID, "conn", cID).
			Errorf("failed to read first message from websocket, reason=%v", err)
		return
	}

	var p RDCleanPathPdu
	if err := unmarshalContextExplicit(msg, &p); err != nil {
		log.With("sid", sessionID, "conn", cID).
			Errorf("failed to read first message from websocket, reason=%v", err)
		return
	}

	cppVersion := p.Version
	log := log.With("sid", sessionID, "conn", cID, "cppVersion", cppVersion)

	ctxDuration, dba, connectionModel, tokenVerifier, extractedCreds, err := checkAndPrepareRDP(p.X224ConnectionPDU)
	if errors.Is(err, models.ErrNotFound) {
		response := RDCleanPathPdu{
			Version:           cppVersion,
			Error:             NewRDCleanPathError(403),
			X224ConnectionPDU: buildGenericRdpErrorPacket(),
		}
		pkt, err := response.Encode()
		if err != nil {
			log.Errorf("failed to encode RDP error packet: %v", err)
			return
		}
		err = ws.WriteMessage(websocket.BinaryMessage, pkt)
		if err != nil {
			log.Errorf("failed to write RDP error packet to client: %v", err)
		}
		return
	}

	if err != nil {
		log.Errorf("failed to check and prepare RDP: %s", err)
		return
	}

	var serverCertChain [][]byte
	session, err := broker.CreateRDPSession(
		nil,
		*connectionModel,
		peerAddr,
		broker.ProtocolRDP,
		extractedCreds,
		dba.ExpireAt,
		ctxDuration,
	)

	if err != nil {
		log.Errorf("Failed to create session: %v", err)
		return
	}

	if session == nil {
		log.Errorf("CreateSession returned nil session")
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

	sessionConn := session.ToConn()
	defer sessionConn.Close()

	buffer := make([]byte, 16384)

	log.Debugf("Sending X224 Connection PDU to agent")
	_, err = sessionConn.Write(p.X224ConnectionPDU)
	if err != nil {
		log.Errorf("Failed to write X224: %v", err)
		return
	}
	_ = sessionConn.SetDeadline(time.Now().UTC().Add(time.Second * 2))
	n, err := sessionConn.Read(buffer)
	if err != nil {
		log.Errorf("Failed to read X224 response: %v", err)
		return
	}
	// Now, agentrs expects a TLS Connection. So we start the handshake here
	// The IronRDP Web assumes we're already in a TLS connection, so we need to
	// "unwrap" the connection to it.
	tlsClient := tls.Client(sessionConn, &tls.Config{
		InsecureSkipVerify: true,
	})
	defer tlsClient.Close() // Tecnically, we don't need to close this

	log.Debugf("Perfoming Handshake")
	err = tlsClient.Handshake()
	if err != nil {
		log.Errorf("Failed to perform handshake: %v", err)
		return
	}

	// Replace serverCertChain with what you get on tlsClient
	// CredSSP uses public key for negotiating RDP Session Key
	serverCertChain = nil
	for _, cert := range tlsClient.ConnectionState().PeerCertificates {
		serverCertChain = append(serverCertChain, cert.Raw)
	}

	// Get server name from TLS connection state
	// It doesnt actually need to be that, but just to not leave it empty
	serverName := tlsClient.ConnectionState().ServerName
	packet := &RDCleanPathPdu{
		Version:           cppVersion,
		ServerAddr:        &serverName,
		ServerCertChain:   serverCertChain,
		X224ConnectionPDU: buffer[:n],
	}

	log.Debugf("Sending RDCleanPathPdu")
	pkt, err := packet.Encode()
	if err != nil {
		log.Errorf("Failed to encode packet: %v", err)
		return
	}

	err = ws.WriteMessage(websocket.BinaryMessage, pkt)
	if err != nil {
		log.Errorf("Failed to write message: %v", err)
		return
	}

	// From here on, its standard TCP RDP flow, so we just
	// pipe all data between WebSocket and TLS connection

	go func() {
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				log.Errorf("Failed to read message: %v", err)
				return
			}

			if _, err = tlsClient.Write(msg); err != nil {
				log.Errorf("Failed to write message: %v", err)
				return
			}
		}
	}()

	for {
		n, err = tlsClient.Read(buffer)
		if err != nil {
			log.Errorf("Failed to read message: %v", err)
			break
		}
		err = ws.WriteMessage(websocket.BinaryMessage, buffer[:n])
		if err != nil {
			log.Errorf("Failed to write message: %v", err)
			break
		}
	}

	log.Infof("Iron Session closed")
}
