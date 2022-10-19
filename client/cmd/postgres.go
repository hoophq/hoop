package cmd

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"time"

	"github.com/briandowns/spinner"
	"github.com/google/uuid"
	"github.com/runopsio/hoop/client/grpc"
	"github.com/runopsio/hoop/common/memory"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/spf13/cobra"
)

var postgresCmd = &cobra.Command{
	Use:          "postgres CONNECTION",
	Short:        "Connect to a postgres server",
	Aliases:      []string{"pg"},
	SilenceUsage: false,
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) != 1 {
			cmd.Usage()
			os.Exit(1)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		loader := spinner.New(spinner.CharSets[78], 70*time.Millisecond)
		loader.Color("yellow")
		loader.Start()
		loader.Suffix = " connecting to gateway..."

		client, err := grpc.ConnectGrpc(args[0], pb.ProtocolPostgresType)
		loader.Disable()
		if err != nil {
			log.Fatal(err)
			return
		}
		log.Fatal(runPGProxy(proxyPort, client))
	},
}

func init() {
	postgresCmd.Flags().StringVarP(&proxyPort, "proxy-port", "p", "5433", "The port to bind the postgres proxy")
	rootCmd.AddCommand(postgresCmd)
}

func runPGProxy(proxyPort string, grpcClient *grpc.Client) error {
	proxy := PG{
		ProxyPort: proxyPort,
		cli:       grpcClient,
		connStore: memory.New(),
	}
	return proxy.Serve()
}

type PG struct {
	ProxyPort string
	cli       *grpc.Client
	connStore memory.Store
}

func (p *PG) GatewayLocalConnectionID() string {
	obj := p.connStore.Get(pb.SpecGatewayConnectionID)
	gwID, _ := obj.(string)
	if gwID == "" {
		// TODO: log/warn
		log.Printf("gateway id is empty")
	}
	return gwID
}

func (p *PG) SetGatewayLocalConnectionID(gwID string) {
	p.connStore.Set(pb.SpecGatewayConnectionID, gwID)
}

func (p *PG) GetConnection(pkt *pb.Packet) io.WriteCloser {
	if pkt.Spec == nil {
		return nil
	}
	clientConnectionID := pkt.Spec[pb.SpecClientConnectionID]
	if clientConnectionID == nil {
		// TODO: warn empty
		return nil
	}
	obj := p.connStore.Get(string(clientConnectionID))
	c, _ := obj.(io.WriteCloser)
	return c
}

func (p PG) Serve() error {
	listenAddr := fmt.Sprintf("127.0.0.1:%s", p.ProxyPort)
	lis, err := net.Listen("tcp4", listenAddr)
	if err != nil {
		return fmt.Errorf("failed listening to address %v, err=%v", listenAddr, err)
	}
	go func() {
		for {
			pkt, err := p.cli.Stream.Recv()
			if err == io.EOF {
				// TODO: close channel
				log.Fatal("end of program, EOF")
			}
			if err != nil {
				// TODO: close channel
				log.Fatalf("closing client proxy, err=%v", err)
			}
			if pb.PacketType(pkt.Type) == pb.PacketPGWriteClientType {
				p.processPacket(pkt)
				continue
			}
			log.Printf("receive packet type [%s]", pkt.Type)
			go p.processPacket(pkt)
		}
	}()

	if err := p.cli.Stream.Send(&pb.Packet{
		Type: pb.PacketGatewayConnectType.String(),
	}); err != nil {
		log.Fatalf("failed connecting to gateway, err=%v", err)
	}

	for {
		pgClient, err := lis.Accept()
		if err != nil {
			log.Fatalf("listener accept err: %s\n", err)
		}
		go p.serveConn(pgClient)
	}
}
func (p *PG) serveConn(pgClient net.Conn) {
	localGatewayConnectionID := p.GatewayLocalConnectionID()
	if localGatewayConnectionID == "" {
		log.Fatalf("could not connect to gateway")
	}
	connectionID := uuid.NewString()
	defer func() {
		log.Printf("gatewayid=%s - closing tcp connection %s, remote=%s",
			localGatewayConnectionID, connectionID, pgClient.RemoteAddr())
		p.connStore.Del(connectionID)
		p.connStore.Del(localGatewayConnectionID)
		if err := pgClient.Close(); err != nil {
			// TODO: log warn
			log.Printf("failed closing client connection, err=%v", err)
		}
	}()
	donec := make(chan struct{})
	connWrapper := pb.NewConnectionWrapper(pgClient, donec)
	p.connStore.Set(connectionID, connWrapper)

	log.Printf("gatewayid=%v - connected pg client=%s, tcp-conn=%s", localGatewayConnectionID, pgClient.RemoteAddr(), connectionID)
	pgServerWriter := pb.NewStreamWriter(p.cli.Stream.Send, pb.PacketPGWriteServerType, map[string][]byte{
		string(pb.SpecClientConnectionID):  []byte(connectionID),
		string(pb.SpecGatewayConnectionID): []byte(localGatewayConnectionID),
	})
	if _, err := io.CopyBuffer(pgServerWriter, pgClient, nil); err != nil {
		log.Printf("failed copying buffer, err=%v", err)
		connWrapper.Close()
	}
	// wait until this connection is done!
	<-donec
}

func (p *PG) processPacket(pkt *pb.Packet) {
	p.processAgentConnectPhase(pkt)
	p.processPGPackets(pkt)
}

// processAgentConnectPhase is initiated when the proxy tries to establish
// a connection with the remote target. The agent could be disconnected or
// the Postgres could not be reached, this phase validates these conditions.
func (p *PG) processAgentConnectPhase(pkt *pb.Packet) {
	switch pb.PacketType(pkt.Type) {
	case pb.PacketGatewayConnectOKType:
		gwID, ok := pkt.Spec[pb.SpecGatewayConnectionID]
		if !ok || gwID == nil {
			// TODO return internal error to client
			return
		}
		p.SetGatewayLocalConnectionID(string(gwID))
		log.Printf("gatewayid=%s - ready to accept connections at 127.0.0.1:%s", string(gwID), p.ProxyPort)
	case pb.PacketGatewayConnectErrType:
		gwID := pkt.Spec[pb.SpecGatewayConnectionID]
		log.Fatalf("gatewayid=%s - found an error connecting with gateway, err=%v",
			string(gwID), string(pkt.GetPayload()))
	}
}

func (p *PG) processPGPackets(pkt *pb.Packet) {
	switch pb.PacketType(pkt.Type) {
	case pb.PacketCloseConnectionType:
		gwID := pkt.Spec[pb.SpecGatewayConnectionID]
		log.Printf("gatewayid=%s - received a disconnect, reason=%v", string(gwID), string(pkt.GetPayload()))
		if conn := p.GetConnection(pkt); conn != nil {
			conn.Close()
		}
		os.Exit(0)
	case pb.PacketPGWriteClientType:
		conn := p.GetConnection(pkt)
		if conn == nil {
			log.Println("connection is empty")
			return
		}
		if _, err := conn.Write(pkt.GetPayload()); err != nil {
			// TODO: log error
			conn.Close()
			log.Fatalf("failed writing to client, err=%v", err)
		}
	}
}
