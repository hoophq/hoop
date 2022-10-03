package agent

import (
	"context"
	"io"
	"log"

	"github.com/runopsio/hoop/common/pg"
	"github.com/runopsio/hoop/common/pg/middlewares"
	pgtypes "github.com/runopsio/hoop/common/pg/types"
	pb "github.com/runopsio/hoop/common/proto"
)

func (a *Agent) processPGProtocol(pkt *pb.Packet) {
	if pb.PacketType(pkt.Type) != pb.PacketPGWriteServerType {
		return
	}
	gwID := pkt.Spec[pb.SpecGatewayConnectionID]
	swPgClient := pb.NewStreamWriter(a.stream.Send, pb.PacketPGWriteClientType, pkt.Spec)
	envObj := a.connStore.Get(string(gwID))
	pgEnv, _ := envObj.(*pgEnv)
	if pgEnv == nil {
		log.Println("postgres credentials not found in memory")
		writePGClientErr(swPgClient,
			pg.NewFatalError("credentials is empty, contact the administrator").Encode())
		return
	}
	clientConnectionID := pkt.Spec[pb.SpecClientConnectionID]
	clientObj := a.connStore.Get(string(clientConnectionID))
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
		err := a.stream.Send(&pb.Packet{
			Type:    pb.PacketPGWriteClientType.String(),
			Spec:    pkt.Spec,
			Payload: []byte{pgtypes.ServerSSLNotSupported.Byte()},
		})
		if err != nil {
			log.Printf("failed sending ssl response back, err=%v", err)
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

	log.Printf("starting postgres connection for %s", gwID)
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
	pg.NewProxy(context.Background(), swPgClient, mid.ProxyCustomAuth).
		RunWithReader(pg.NewReader(pgServer))
	proxyServerWriter := pg.NewProxy(context.Background(), pgServer, mid.DenyChangePassword).Run()
	a.connStore.Set(string(clientConnectionID), proxyServerWriter)
}

func writePGClientErr(w io.Writer, msg []byte) {
	if _, err := w.Write(msg); err != nil {
		log.Printf("failed writing error back to client, err=%v", err)
	}
}
