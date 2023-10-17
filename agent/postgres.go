package agent

import (
	"context"
	"fmt"
	"io"

	"github.com/hoophq/pluginhooks"
	"github.com/runopsio/hoop/agent/dlp"
	"github.com/runopsio/hoop/agent/pg"
	"github.com/runopsio/hoop/agent/pg/middlewares"
	"github.com/runopsio/hoop/common/log"
	pgtypes "github.com/runopsio/hoop/common/pg"
	pb "github.com/runopsio/hoop/common/proto"
	pbclient "github.com/runopsio/hoop/common/proto/client"
)

func (a *Agent) processPGProtocol(pkt *pb.Packet) {
	sessionID := string(pkt.Spec[pb.SpecGatewaySessionID])
	swPgClient := pb.NewStreamWriter(a.client, pbclient.PGConnectionWrite, pkt.Spec)
	connParams, pluginHooks := a.connectionParams(sessionID)
	if connParams == nil {
		log.Printf("session=%s - connection params not found", sessionID)
		a.writePGClientErr(sessionID, swPgClient,
			"connection params not found, contact the administrator")
		return
	}

	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	if clientConnectionID == "" {
		log.Println("connection id not found in memory")
		a.writePGClientErr(sessionID, swPgClient,
			"connection id not found, contact the administrator")
		return
	}
	clientConnectionIDKey := fmt.Sprintf("%s:%s", sessionID, string(clientConnectionID))
	clientObj := a.connStore.Get(clientConnectionIDKey)
	if proxyServerWriter, ok := clientObj.(pg.Proxy); ok {
		mutatePayload, err := pluginHooks.ExecRPCOnRecv(&pluginhooks.Request{
			SessionID:  sessionID,
			PacketType: pkt.Type,
			Payload:    pkt.Payload,
		})
		if err != nil {
			msg := fmt.Sprintf("plugin error, failed processing it, reason=%v", err)
			log.Println(msg)
			a.writePGClientErr(sessionID, swPgClient, msg)
			return
		}
		if len(mutatePayload) > 0 {
			pkt.Payload = mutatePayload
		}
		if err := proxyServerWriter.Send(pkt.Payload); err != nil {
			log.Println(err)
			proxyServerWriter.Cancel()
		}
		return
	}

	connenv, err := parseConnectionEnvVars(connParams.EnvVars, pb.ConnectionTypePostgres)
	if err != nil {
		log.Warnf("postgres credentials not found in memory, err=%v", err)
		a.writePGClientErr(sessionID, swPgClient,
			"credentials are empty, contact the administrator")
		return
	}

	// startup phase
	_, pgPkt, err := pg.DecodeStartupPacket(pb.BufferedPayload(pkt.Payload))
	if err != nil {
		log.Printf("failed decoding startup packet: %v", err)
		a.writePGClientErr(sessionID, swPgClient,
			"failed decoding startup packet (1), contact the administrator")
		return
	}

	if pgPkt.IsFrontendSSLRequest() {
		err := a.client.Send(&pb.Packet{
			Type:    pbclient.PGConnectionWrite,
			Spec:    pkt.Spec,
			Payload: []byte{pgtypes.ServerSSLNotSupported.Byte()},
		})
		if err != nil {
			log.Printf("failed sending ssl response back, err=%v", err)
		}
		return
	}

	// https://www.postgresql.org/docs/current/protocol-flow.html#id-1.10.6.7.10
	if pgPkt.IsCancelRequest() {
		// TODO(san): send the packet back to the connection which initiate the cancel request.
		// Storing the PID in memory may allow to track the connection between client/agent
		log.Printf("session=%v - starting cancel request", sessionID)
		pgServer, err := newTCPConn(connenv.host, connenv.port)
		if err != nil {
			log.Printf("failed creating a cancel connection with postgres server, err=%v", err)
			return
		}
		defer pgServer.Close()
		if _, err := pgServer.Write(pgPkt.Encode()); err != nil {
			log.Printf("failed sending cancel request, err=%v", err)
			a.writePGClientErr(sessionID, swPgClient,
				"failed canceling request in the postgres server")
		}
		return
	}

	startupPkt, err := pg.DecodeStartupPacketWithUsername(pb.BufferedPayload(pkt.Payload), connenv.user)
	if err != nil {
		log.Printf("failed decoding startup packet with username, err=%v", err)
		a.writePGClientErr(sessionID, swPgClient,
			"failed decoding startup packet (2), contact the administrator")
		return
	}

	log.Printf("starting postgres connection for %s", sessionID)
	pgServer, err := newTCPConn(connenv.host, connenv.port)
	if err != nil {
		log.Errorf("failed obtaining connection with postgres server, err=%v", err)
		a.writePGClientErr(sessionID, swPgClient,
			"failed connecting with postgres server, contact the administrator")
		return
	}
	if _, err := pgServer.Write(startupPkt.Encode()); err != nil {
		log.Printf("failed writing startup packet, err=%v", err)
		a.writePGClientErr(sessionID, swPgClient,
			"failed writing startup packet, contact the administrator")
		return
	}
	log.Println("finish startup phase")
	mid := middlewares.New(swPgClient, pgServer, connenv.user, connenv.pass)
	var dlpClient dlp.Client
	if dlpc, ok := a.connStore.Get(dlpClientKey).(dlp.Client); ok {
		dlpClient = dlpc
	}
	redactmiddleware, err := dlp.NewRedactMiddleware(dlpClient, connParams.DLPInfoTypes...)
	if err != nil {
		log.Printf("failed creating redact middleware, err=%v", err)
		a.writePGClientErr(sessionID, swPgClient,
			"failed initalizing postgres proxy, contact the administrator")
		return
	}

	swHookPgClient := pb.NewHookStreamWriter(a.client, pbclient.PGConnectionWrite, pkt.Spec, pluginHooks)
	pg.NewProxy(
		pg.NewContext(context.Background(), sessionID),
		swHookPgClient,
		mid.ProxyCustomAuth,
		redactmiddleware.Handler,
	).RunWithReader(pgServer)
	proxyServerWriter := pg.NewProxy(
		pg.NewContext(context.Background(), sessionID),
		pgServer,
		mid.DenyChangePassword,
	).Run()
	a.connStore.Set(clientConnectionIDKey, proxyServerWriter)
}

func (a *Agent) writePGClientErr(sessionID string, w io.Writer, errMsg string) {
	if _, err := w.Write(pg.NewFatalError(errMsg).Encode()); err != nil {
		log.Printf("failed writing error back to client, err=%v", err)
	}
	a.sendClientSessionClose(sessionID, errMsg)
}
