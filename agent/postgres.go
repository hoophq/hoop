package agent

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/runopsio/hoop/agent/pg"
	"github.com/runopsio/hoop/agent/pg/middlewares"
	pb "github.com/runopsio/hoop/common/proto"
	pgtypes "github.com/runopsio/hoop/common/proxy"
)

func (a *Agent) processPGProtocol(pkt *pb.Packet) {
	sessionID := pkt.Spec[pb.SpecGatewaySessionID]
	swPgClient := pb.NewStreamWriter(a.client, pb.PacketPGWriteClientType, pkt.Spec)
	envObj := a.connStore.Get(string(sessionID))
	pgEnv, _ := envObj.(*pgEnv)
	if pgEnv == nil {
		log.Println("postgres credentials not found in memory")
		writePGClientErr(swPgClient,
			pg.NewFatalError("credentials is empty, contact the administrator").Encode())
		return
	}
	clientConnectionID := string(pkt.Spec[pb.SpecClientConnectionID])
	if clientConnectionID == "" {
		log.Println("connection id not found in memory")
		writePGClientErr(swPgClient,
			pg.NewFatalError("connection id not found, contact the administrator").Encode())
		return
	}
	clientConnectionIDKey := fmt.Sprintf("%s:%s", string(sessionID), string(clientConnectionID))
	clientObj := a.connStore.Get(clientConnectionIDKey)
	if proxyServerWriter, ok := clientObj.(pg.Proxy); ok {
		if err := proxyServerWriter.Send(pkt.Payload); err != nil {
			log.Println(err)
			proxyServerWriter.Cancel()
		}
		return
	}
	// startup phase
	_, pgPkt, err := pg.DecodeStartupPacket(pb.BufferedPayload(pkt.Payload))
	if err != nil {
		log.Printf("failed decoding startup packet: %v", err)
		writePGClientErr(swPgClient,
			pg.NewFatalError("failed decoding startup packet (1), contact the administrator").Encode())
		return
	}

	if pgPkt.IsFrontendSSLRequest() {
		err := a.client.Send(&pb.Packet{
			Type:    pb.PacketPGWriteClientType.String(),
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
		pgServer, err := newTCPConn(pgEnv.host, pgEnv.port)
		if err != nil {
			log.Printf("failed creating a cancel connection with postgres server, err=%v", err)
			return
		}
		defer pgServer.Close()
		if _, err := pgServer.Write(pgPkt.Encode()); err != nil {
			log.Printf("failed sending cancel request, err=%v", err)
			writePGClientErr(swPgClient,
				pg.NewFatalError("failed canceling request in the postgres server").Encode())
		}
		return
	}

	startupPkt, err := pg.DecodeStartupPacketWithUsername(pb.BufferedPayload(pkt.Payload), pgEnv.user)
	if err != nil {
		log.Printf("failed decoding startup packet with username, err=%v", err)
		writePGClientErr(swPgClient,
			pg.NewFatalError("failed decoding startup packet (2), contact the administrator").Encode())
		return
	}

	log.Printf("starting postgres connection for %s", sessionID)
	pgServer, err := newTCPConn(pgEnv.host, pgEnv.port)
	if err != nil {
		log.Printf("failed obtaining connection with postgres server, err=%v", err)
		writePGClientErr(swPgClient,
			pg.NewFatalError("failed connecting with postgres server, contact the administrator").Encode())
		return
	}
	if _, err := pgServer.Write(startupPkt.Encode()); err != nil {
		log.Printf("failed writing startup packet, err=%v", err)
		writePGClientErr(swPgClient,
			pg.NewFatalError("failed writing startup packet, contact the administrator").Encode())
	}
	log.Println("finish startup phase")
	mid := middlewares.New(swPgClient, pgServer, pgEnv.user, pgEnv.pass)

	pg.NewProxy(
		pg.NewContext(context.Background(), string(sessionID)),
		swPgClient,
		mid.ProxyCustomAuth,
	).RunWithReader(pg.NewReader(pgServer))
	proxyServerWriter := pg.NewProxy(
		pg.NewContext(context.Background(), string(sessionID)),
		pgServer,
		mid.DenyChangePassword,
	).Run()
	a.connStore.Set(clientConnectionIDKey, proxyServerWriter)
}

func writePGClientErr(w io.Writer, msg []byte) {
	if _, err := w.Write(msg); err != nil {
		log.Printf("failed writing error back to client, err=%v", err)
	}
}
