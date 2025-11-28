package ssmproxy

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aws/session-manager-plugin/src/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/hoophq/hoop/common/grpc"
	"github.com/hoophq/hoop/common/log"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/proxyproto/grpckey"
)

var (
	store       = sync.Map{}
	instanceKey = "ssm_instance"
	upgrader    = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
)

type SSMProxy struct {
	listener    net.Listener
	listenAddr  string
	router      gin.IRouter
	connections atomic.Int32
}

// GetServerInstance returns the singleton instance of SSMServer.
func GetServerInstance() *SSMProxy {
	server, _ := store.LoadOrStore(instanceKey, &SSMProxy{})
	return server.(*SSMProxy)
}

func (r *SSMProxy) AttachHandlers(router gin.IRouter) {
	r.router = router
	router.Handle(http.MethodGet, "/", func(c *gin.Context) {
		c.String(http.StatusBadRequest, "Invalid request")
	})
	router.Handle(http.MethodPost, "/", r.handleStartSession)
	router.Handle(http.MethodGet, "/ws/:connectionId", r.handleWebsocket)
}

func (r *SSMProxy) handleStartSession(c *gin.Context) {
	// X-Amz-Target
	xAmzTarget := c.GetHeader("X-Amz-Target")
	if xAmzTarget != "AmazonSSM.StartSession" {
		c.String(http.StatusBadRequest, "Invalid request")
		return
	}

	authorization := c.GetHeader("Authorization")
	aws4, err := parseAWS4Header(authorization)
	if err != nil {
		log.Errorf("failed to parse AWS4 header, reason=%v", err)
		c.String(http.StatusBadRequest, "Invalid request")
		return
	}

	connectionId, err := AccessKeyToUUID(aws4.AccessKey)
	if err != nil {
		log.Errorf("failed to convert access key to UUID, reason=%v", err)
		c.String(http.StatusBadRequest, "Invalid request")
	}

	dba, err := models.GetConnectionByTypeAndID(pb.ConnectionTypeSSM.String(), connectionId)
	if err != nil {
		log.Errorf("failed to get connection by id, reason=%v", err)
		c.String(http.StatusBadRequest, "Invalid request")
		return
	}

	if len(dba.SecretKeyHash) < 40 {
		// Realistically, this should never happen
		log.Errorf("invalid secret key hash, reason=%v", err)
		c.String(http.StatusInternalServerError, "Internal server error")
	}

	secretKey := dba.SecretKeyHash[:40] // Trimmed secret key since AWS only handles 40 characters

	if !validateAWS4Signature(c, secretKey, aws4) {
		c.String(http.StatusBadRequest, "Invalid request")
		return
	}

	// Get Target from body JSON
	var target ssmStartSessionPacket
	if err := c.BindJSON(&target); err != nil {
		log.Errorf("failed to bind JSON, reason=%v", err)
		c.String(http.StatusBadRequest, "Invalid request")
		return
	}

	// Get host and port from connection to pass as target websocket url
	host := c.Request.Host
	schema := c.Request.URL.Scheme
	if schema == "http" {
		schema = "ws"
	} else {
		schema = "wss"
	}

	token, err := createTokenForConnection(connectionId)
	if err != nil {
		log.Errorf("failed to create token for connection, reason=%v", err)
		c.String(http.StatusInternalServerError, "Failed to create connection")
		return
	}
	targetUrl := fmt.Sprintf("%s://%s/ssm/ws/%s?target=%s", schema, host, connectionId, target.Target)
	c.JSON(http.StatusOK, ssmStartSessionResponsePacket{
		SessionId:  connectionId,
		StreamUrl:  targetUrl,
		TokenValue: token,
	})
}

func (r *SSMProxy) handleWebsocket(c *gin.Context) {
	// Since GIN is co-routine backed and this connection will be kept open
	// we will handle everything here

	connectionId := c.Param("connectionId")
	if connectionId == "" {
		c.String(http.StatusBadRequest, "Invalid request")
		return
	}
	target := c.Query("target")
	if target == "" {
		c.String(http.StatusBadRequest, "Invalid request")
		return
	}

	// Generate unique connection id
	connId := r.connections.Add(1)
	defer func() {
		r.connections.Add(-1)
	}()

	cID := strconv.Itoa(int(connId))
	sessionID := uuid.NewString()

	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.With("sid", sessionID, "conn", cID).
			Errorf("failed to upgrade websocket connection, reason=%v", err)
		c.String(http.StatusInternalServerError, "Failed to upgrade websocket")
		return
	}

	defer ws.Close()

	// Receive the first message
	msgType, msg, err := ws.ReadMessage()
	if err != nil {
		log.With("sid", sessionID, "conn", cID).
			Errorf("failed to read first message from websocket, reason=%v", err)
		return
	}

	// msg is a JSON for ssmInitWebsocketPacket
	var initPacket service.OpenDataChannelInput
	if err := json.Unmarshal(msg, &initPacket); err != nil {
		log.With("sid", sessionID, "conn", cID).
			Errorf("failed to unmarshal init packet, reason=%v", err)
		return
	}

	if initPacket.TokenValue == nil {
		log.With("sid", sessionID, "conn", cID).
			Errorf("invalid token, reason=%v", err)
	}

	// Try parse token
	tokenConnectionId, err := decodeToken(*initPacket.TokenValue)
	if err != nil {
		log.With("sid", sessionID, "conn", cID).
			Errorf("failed to decode token, reason=%v", err)
		return
	}

	if tokenConnectionId != connectionId {
		log.With("sid", sessionID, "conn", cID).
			Errorf("invalid token, expected=%s, got=%s", connectionId, tokenConnectionId)
		return
	}

	log.With("sid", sessionID, "conn", cID).
		Infof("connection established for connectionId=%s, target=%s, awsClientId=%s, awsClientVersion=%s",
			connectionId, target, initPacket.ClientId, initPacket.ClientVersion)

	dbConnection, err := models.GetConnectionByTypeAndID(pb.ConnectionTypeSSM.String(), connectionId)
	if err != nil {
		log.With("sid", sessionID, "conn", cID).
			Errorf("failed to get connection by id, reason=%v", err)
		c.String(http.StatusBadRequest, "Invalid request")
		return
	}
	log.With("sid", sessionID, "conn", cID).
		Infof("starting websocket connection for connectionId=%s, target=%s, sessionID=%s", connectionId, target, sessionID)

	client, err := grpc.Connect(grpc.ClientConfig{
		ServerAddress: grpc.LocalhostAddr,
		Token:         "", // it will use impersonate-auth-key as authentication
		UserAgent:     "ssm/grpc",
		Insecure:      appconfig.Get().GatewayUseTLS() == false,
		TLSCA:         appconfig.Get().GrpcClientTLSCa(),
		// it should be safe to skip verify here as we are connecting to localhost
		TLSSkipVerify: true,
	},
		grpc.WithOption(grpc.OptionConnectionName, dbConnection.ConnectionName),
		grpc.WithOption(grpckey.ImpersonateAuthKeyHeaderKey, grpckey.ImpersonateSecretKey),
		grpc.WithOption(grpckey.ImpersonateUserSubjectHeaderKey, dbConnection.UserSubject),
		grpc.WithOption("origin", pb.ConnectionOriginClient),
		grpc.WithOption("verb", pb.ClientVerbConnect),
		grpc.WithOption("session-id", sessionID),
	)
	if err != nil {
		log.With("sid", sessionID, "conn", cID).Errorf("failed connecting to hoop server, reason=%v", err)
		c.String(http.StatusInternalServerError, "Failed to connect to hoop server")
	}
	defer client.Close()

	// Send an open session packet
	err = client.Send(&pb.Packet{
		Type: pbagent.SessionOpen,
		Spec: map[string][]byte{
			pb.SpecGatewaySessionID:   []byte(sessionID),
			pb.SpecClientConnectionID: []byte(cID),
		},
	})
	if err != nil {
		log.With("sid", sessionID, "conn", cID).
			Errorf("failed sending open session packet to hoop server, reason=%v", err)
		return
	}

	// Wait for session open confirmation
	pkt, err := client.Recv()
	if err != nil {
		log.With("sid", sessionID, "conn", cID).
			Errorf("failed receiving session open confirmation from hoop server, reason=%v", err)
		return
	}

	connectionType := pb.ConnectionType(pkt.Spec[pb.SpecConnectionType])
	if pkt.Type != pbclient.SessionOpenOK || connectionType != pb.ConnectionTypeSSM {
		log.With("sid", sessionID, "conn", cID).
			Errorf("unsupported connection type, got=%v", connectionType)
		return
	}

	log.With("sid", sessionID, "conn", cID).Debugf("Starting pipes")
	err = sendWebsocketMessageHelper(client, msgType, msg, target, sessionID, cID)
	if err != nil {
		log.With("sid", sessionID, "conn", cID).
			Errorf("error sending initial websocket message: %v", err)
		return
	}

	// Ready for pumping
	// Start RX Pipe (Client -> GRPC)
	go r.handleRXPipe(ws, client, target, sessionID, cID)
	// Start TX Pipe (GRPC -> Client)
	r.handleTXPipe(ws, client, sessionID, cID)

	log.With("sid", sessionID, "conn", cID).
		Infof("connection closed for connectionId=%s, target=%s, sessionID=%s", connectionId, target, sessionID)
}

func (r *SSMProxy) handleTXPipe(ws *websocket.Conn, client pb.ClientTransport, sessionID, cID string) {
	defer ws.Close()
	defer client.Close()
	// We need to read GRPC from another routine and pipe channel here
	// Because otherwise we can't do websocket ping packets.
	running := &atomic.Bool{}
	running.Store(true)
	packetChan := make(chan *pb.Packet)

	const wsPingInterval = time.Second * 5

	ticker := time.NewTicker(wsPingInterval)
	defer ticker.Stop()

	go func() {
		for running.Load() {
			msg, err := client.Recv()
			if err != nil {
				log.With("sid", sessionID, "conn", cID).Errorf("failed to receive packet from hoop server, reason=%v", err)
				running.Store(false)
			}
			packetChan <- msg
		}
	}()

	for running.Load() {
		select {
		case msg := <-packetChan:
			switch msg.Type {
			case pbclient.SSMConnectionWrite:
				err := ws.WriteMessage(int(msg.Spec[pb.SpecAwsSSMWebsocketMsgType][0]), msg.Payload)
				if err != nil {
					log.Errorf("failed to write message to websocket, reason=%v", err)
					break
				}

			case pbclient.TCPConnectionClose, pbclient.SessionClose:
				log.With("sid", sessionID, "conn", cID).Infof("connection closed by server, payload=%v", string(msg.Payload))
				return

			default:
				log.With("sid", sessionID, "conn", cID).Errorf("received invalid packet type %T", msg.Type)
				return
			}
		case <-ticker.C:
			if err := ws.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(wsPingInterval/2)); err != nil {
				log.With("sid", sessionID, "conn", cID).Errorf("ws ping timeout")
				return
			}
			// We could use default: here to allow the running flag to be read
			// But if something stopped, the ws ping will trigger every 5 seconds
			// So worse case scenario, this routine will take 5 seconds to die
		}

	}
}

func (r *SSMProxy) handleRXPipe(ws *websocket.Conn, client pb.ClientTransport, target, sessionID, cID string) {
	defer ws.Close()
	defer client.Close()

	for {
		msgType, msgData, err := ws.ReadMessage()
		if err != nil {
			log.With("sid", sessionID, "conn", cID).
				Errorf("failed to read websocket message, reason=%v", err)
			break
		}
		err = sendWebsocketMessageHelper(client, msgType, msgData, target, sessionID, cID)
		if err != nil {
			log.With("sid", sessionID, "conn", cID).
				Errorf("failed to send packet to hoop server, reason=%v", err)
			break
		}
	}
}

func sendWebsocketMessageHelper(client pb.ClientTransport, msgType int, msgData []byte, target, sessionID, cID string) error {
	encodedType := make([]byte, 4)
	binary.LittleEndian.PutUint32(encodedType, uint32(msgType))

	return client.Send(&pb.Packet{
		Type:    pbagent.SSMConnectionWrite,
		Payload: msgData,
		Spec: map[string][]byte{
			pb.SpecAwsSSMWebsocketMsgType: encodedType,
			pb.SpecGatewaySessionID:       []byte(sessionID),
			pb.SpecClientConnectionID:     []byte(cID),
			pb.SpecAwsSSMEc2InstanceId:    []byte(target),
		},
	})
}
