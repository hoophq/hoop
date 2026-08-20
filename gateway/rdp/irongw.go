package rdp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/hoophq/hoop/common/featureflag"
	"github.com/hoophq/hoop/common/keys"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/broker"
	"github.com/hoophq/hoop/gateway/idp"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/transport/usertoken"
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

func isRDPDesktopDimension(value string) bool {
	if len(value) == 0 || len(value) > 4 {
		return false
	}
	for i := range len(value) {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func invalidRDPDesktopSizeDimensions(preset string) (width, height int, ok bool) {
	widthText, heightText, found := strings.Cut(preset, "x")
	if !found || !isRDPDesktopDimension(widthText) || !isRDPDesktopDimension(heightText) {
		return 0, 0, false
	}

	width, widthErr := strconv.Atoi(widthText)
	height, heightErr := strconv.Atoi(heightText)
	if widthErr != nil || heightErr != nil || width <= 0 || height <= 0 {
		return 0, 0, false
	}
	return width, height, true
}

func (r *IronRDPGateway) handleClient(c *gin.Context) {
	rdpCredential := c.PostForm("credential")
	if rdpCredential == "" {
		log.Errorf("failed to get credential, reason=empty")
		c.String(http.StatusBadRequest, "Invalid request")
		return
	}
	desktopSizePreset := c.PostForm("desktop_size")
	desktopSize, ok := rdpDesktopSizeFromPreset(desktopSizePreset)
	if !ok {
		if width, height, dimensions := invalidRDPDesktopSizeDimensions(desktopSizePreset); dimensions {
			log.With(
				"desktop-size-category", "unsupported-dimensions",
				"desktop-width", width,
				"desktop-height", height,
			).Warnf("invalid RDP desktop size preset")
		} else {
			log.With(
				"desktop-size-category", "invalid-format",
				"desktop-size-length", len(desktopSizePreset),
			).Warnf("invalid RDP desktop size preset")
		}
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

	if !models.IsMachineIdentityCredential(dba.ID) {
		tokenVerifier, _, err := idp.NewUserInfoTokenVerifierProvider()
		if err != nil {
			log.Errorf("failed to load IDP provider: %v", err)
			c.String(http.StatusBadRequest, "Invalid request")
			return
		}

		if err = usertoken.CheckUserToken(tokenVerifier, dba.UserSubject); err != nil {
			log.Errorf("Error verifying the user token: %v", err)
			c.String(http.StatusBadRequest, "Invalid request")
			return
		}
	}

	// We don't need to do extended checks now because websocket will do it.

	data := renderWebClientTemplate("RDP Connection", rdpCredential, desktopSize)
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(data))
}

// writeRDPClientError sends an RDP-protocol error packet to the web client so
// it shows a connection-refused dialog instead of hanging or failing
// cryptically mid-handshake. The RDP negotiation protocol can only carry a
// generic failure code (not free text), so the human-readable reason is
// logged gateway-side; the client sees a clean refusal. Best-effort: write
// errors are logged, not surfaced.
func writeRDPClientError(ws *websocket.Conn, cppVersion uint64, reason string) {
	log.Infof("refusing RDP connection: %s", reason)
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
	if err := ws.WriteMessage(websocket.BinaryMessage, pkt); err != nil {
		log.Errorf("failed to write RDP error packet to client: %v", err)
	}
}

func handshakeAgentTLS(conn net.Conn) (*tls.Conn, error) {
	tlsClient := tls.Client(conn, &tls.Config{
		InsecureSkipVerify: true,
	})
	if err := tlsClient.Handshake(); err != nil {
		return nil, err
	}
	if err := conn.SetDeadline(time.Time{}); err != nil {
		_ = tlsClient.Close()
		return nil, fmt.Errorf("clear handshake deadline: %w", err)
	}
	return tlsClient, nil
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

	ctxDuration, dba, connectionModel, tokenVerifier, extractedCreds, isMachineCredential, err := checkAndPrepareRDP(p.X224ConnectionPDU)
	if errors.Is(err, models.ErrNotFound) {
		writeRDPClientError(ws, cppVersion, "connection not found")
		return
	}

	if err != nil {
		log.Errorf("failed to check and prepare RDP: %s", err)
		return
	}

	// Resolve the org/user context BEFORE creating the session: the org ID
	// decides whether the agent-side PII guard is enabled, and that decision
	// must be carried in the SessionStarted message the broker sends to the
	// agent on session creation.
	var recorderOrgID, recorderUserID, recorderUserName, recorderUserEmail string
	if isMachineCredential {
		recorderOrgID = dba.OrgID
		recorderUserID = dba.UserSubject
		recorderUserName = "machine-identity"
	} else {
		userCtx, err := models.GetUserContext(dba.UserSubject)
		if err != nil {
			log.Errorf("Failed to get user context: %v", err)
			return
		}
		recorderOrgID = userCtx.OrgID
		recorderUserID = userCtx.UserID
		recorderUserName = userCtx.UserName
		recorderUserEmail = userCtx.UserEmail
	}

	// The rollout flag enables the RDP guard; the resource's DataMaskingRules
	// decide whether this connection is masked and supply its entity selection
	// and confidence threshold.
	var maskingParams *rdpDataMaskingParams
	if featureflag.IsEnabled(recorderOrgID, PIIGateFlagName) {
		maskingParams, err = dataMaskingParamsForConnection(recorderOrgID, connectionModel.Name)
		if err != nil {
			log.With("sid", sessionID, "conn", cID).Errorf("failed loading RDP data masking rules: %v", err)
			writeRDPClientError(ws, cppVersion, "Connection refused: failed to load data masking rules.")
			return
		}
	}

	// When a connection-scoped rule applies, the agent runs the PII gate where
	// the plaintext already flows. The gateway sends the complete resource
	// policy while the agent supplies its local Presidio/OCR endpoints. Masked
	// sessions fail closed unless the selected agent can enforce that policy.
	agentGuard := broker.RDPGuardConfig{Enabled: maskingParams != nil}
	if agentGuard.Enabled {
		capable, known := broker.AgentCapability(connectionModel.AgentName, broker.CapabilitySupportsPIIGuard)
		switch {
		case !known:
			log.Errorf("refusing RDP session: agent %q did not advertise PII guard capability",
				connectionModel.AgentName)
			writeRDPClientError(ws, cppVersion,
				fmt.Sprintf("Connection refused: agent %q must be upgraded to apply this connection's Data Masking rule.",
					connectionModel.AgentName))
			return
		case !capable:
			log.Errorf("refusing RDP session: agent %q cannot enforce the PII guard (missing MSPRESIDIO_ANALYZER_URL and/or RDP_OCR_SERVER_URL)",
				connectionModel.AgentName)
			writeRDPClientError(ws, cppVersion,
				fmt.Sprintf("Connection refused: PII guard is enabled but agent %q is misconfigured (missing OCR/Presidio endpoints).",
					connectionModel.AgentName))
			return
		}

		rulesCapable, rulesKnown := broker.AgentCapability(
			connectionModel.AgentName,
			broker.CapabilitySupportsPIIDataMaskingRules,
		)
		if !rulesKnown || !rulesCapable {
			log.Errorf("refusing RDP session: agent %q does not support complete connection-scoped Data Masking rules",
				connectionModel.AgentName)
			writeRDPClientError(ws, cppVersion,
				fmt.Sprintf("Connection refused: agent %q must be upgraded to apply this connection's Data Masking rule.",
					connectionModel.AgentName))
			return
		}
	}

	if agentGuard.Enabled {
		agentGuard.DataMaskingEntityData = maskingParams.DataMaskingEntityData
		agentGuard.BandPadding = maskingParams.BandPadding
		agentGuard.Policy = appconfig.Get().RDPPIIGuardPolicy()
	}

	var serverCertChain [][]byte
	session, err := broker.CreateRDPSession(
		nil,
		*connectionModel,
		peerAddr,
		broker.ProtocolRDP,
		extractedCreds,
		dba.ID,
		dba.ExpireAt,
		ctxDuration,
		sessionID,
		agentGuard,
	)

	if err != nil {
		log.Errorf("Failed to create session: %v", err)
		return
	}

	if session == nil {
		log.Errorf("CreateSession returned nil session")
		return
	}

	// Initialize RDP session recorder
	recorder := NewRDPSessionRecorder(
		sessionID,
		recorderOrgID,
		recorderUserID,
		recorderUserName,
		recorderUserEmail,
		connectionModel.Name,
		"", // connection subtype
	)

	// Create session in database
	if err := recorder.CreateSession(); err != nil {
		log.Errorf("Failed to create RDP session record: %v", err)
		// Continue anyway - recording is optional
	}

	if !isMachineCredential {
		usertoken.PollingUserToken(context.Background(), func(cause error) {
			session.Close()
		}, tokenVerifier, dba.UserSubject)
	}

	// Clean up session on exit
	var sessionErrMu sync.Mutex
	var sessionErr error
	defer func() {
		if session != nil {
			session.Close()
		}
		// Finalize recording with the error if any
		sessionErrMu.Lock()
		err := sessionErr
		sessionErrMu.Unlock()
		recorder.Close(err)
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
	tlsClient, err := handshakeAgentTLS(sessionConn)
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

	// Record handshake data for session recording
	recorder.RecordHandshake(pkt)

	err = ws.WriteMessage(websocket.BinaryMessage, pkt)
	if err != nil {
		log.Errorf("Failed to write message: %v", err)
		return
	}

	// From here on, its standard TCP RDP flow, so we just
	// pipe all data between WebSocket and TLS connection
	// We also record all traffic for session recording

	// Guarded bytes were already held, analyzed, and redacted by the agent
	// before reaching the gateway. A second gateway-side gate would duplicate
	// OCR/Presidio work and could diverge from the connection's rule policy.
	if agentGuard.Enabled {
		log.With("sid", sessionID).Infof("piigate: agent-side guard active")
	}

	// Use a done channel to signal when the websocket read goroutine exits
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			_, msg, err := ws.ReadMessage()
			if err != nil {
				log.Infof("WebSocket closed: %v", err)
				sessionErrMu.Lock()
				sessionErr = err
				sessionErrMu.Unlock()
				// The outer loop blocks on tlsClient.Read until the agent-side
				// TLS connection drops on its own, which can take tens of
				// seconds (or never on a silent disconnect). Close it here so
				// the outer loop returns immediately and recorder.Close + the
				// session-ended persistence path run promptly.
				_ = tlsClient.Close()
				return
			}

			// Record client -> server traffic (input events)
			recorder.RecordInput(msg)

			if _, err = tlsClient.Write(msg); err != nil {
				log.Errorf("Failed to write message: %v", err)
				sessionErrMu.Lock()
				sessionErr = err
				sessionErrMu.Unlock()
				_ = tlsClient.Close()
				return
			}
		}
	}()

	for {
		n, err = tlsClient.Read(buffer)
		if err != nil {
			log.Infof("TLS connection closed: %v", err)
			sessionErrMu.Lock()
			sessionErr = err
			sessionErrMu.Unlock()
			break
		}

		// Record server -> client traffic (bitmap updates, etc.)
		recorder.RecordOutput(buffer[:n])

		err = ws.WriteMessage(websocket.BinaryMessage, buffer[:n])
		if err != nil {
			log.Errorf("Failed to write message: %v", err)
			sessionErrMu.Lock()
			sessionErr = err
			sessionErrMu.Unlock()
			break
		}
	}

	// Make sure the TLS connection is torn down (idempotent if the goroutine
	// already closed it on browser disconnect) so the websocket-reader goroutine
	// also unblocks and exits.
	_ = tlsClient.Close()

	// Wait for the websocket read goroutine to finish
	<-done

	log.Infof("Iron Session closed")
}
