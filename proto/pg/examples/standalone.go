/**
A proxy example that only dumps packets
**/

package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/runopsio/hoop/proto/pg"
	"github.com/runopsio/hoop/proto/pg/middlewares"
	"github.com/runopsio/hoop/proto/pg/types"
)

const (
	proxyListenAddr = "0.0.0.0:5432"
	postgresPort    = "5444"
	pgUsername      = "bob"
	pgPassword      = "123"
)

func main() {
	Serve()
}

func PGRawConn(host, port string) (net.Conn, error) {
	serverConn, err := net.Dial("tcp4", fmt.Sprintf("%s:%s", host, port))
	if err != nil {
		return nil, fmt.Errorf("failed dialing server: %s", err)
	}
	fmt.Printf("tcp connection stablished with postgres server. address=%v, local-addr=%v\n",
		serverConn.LocalAddr(),
		serverConn.RemoteAddr())
	return serverConn, nil
}

func Serve() {
	lis, err := net.Listen("tcp4", proxyListenAddr)
	fmt.Printf("serving incoming connections %v\n", lis.Addr().String())
	if err != nil {
		panic(err)
	}
	for {
		clientConn, err := lis.Accept()
		if err != nil {
			fmt.Printf("listener accept err: %s\n", err)
			time.Sleep(time.Second * 5)
			continue
		}
		serverConn, err := PGRawConn(os.Getenv("EN0_IP"), postgresPort)
		if err != nil {
			fmt.Println(err)
		}
		go serveConn(clientConn, serverConn)
	}
}

func serveConn(pgClient, pgServer net.Conn) {
	defer pgClient.Close()
	defer pgServer.Close()

	clientReader := pg.NewReader(pgClient)
	serverReader := pg.NewReader(pgServer)
	_, pkt, err := pg.DecodeStartupPacket(clientReader)
	if err != nil {
		fmt.Println(err)
		return
	}

	// https://www.postgresql.org/docs/current/protocol-flow.html#id-1.10.5.7.12
	if pkt.IsFrontendSSLRequest() {
		fmt.Println("--> ssl request packet")
		// intercept and send back to client that the serve does not accept ssl!
		if _, err := pgClient.Write([]byte{types.ServerSSLNotSupported.Byte()}); err != nil {
			log.Println(err)
			return
		}
	}

	fmt.Println("startup packet -->")
	pkt.Dump()
	newPkt, err := pg.DecodeStartupPacketWithUsername(clientReader, pgUsername)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("--> startup, writing to server")
	newPkt.Dump()
	if _, err := pgServer.Write(newPkt.Encode()); err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("copying phase")
	serverRouter := pg.NewProxy(
		context.Background(),
		pgServer,
		middlewares.HexDumpPacket,
	).RunWithReader(clientReader)

	clientRouter := pg.NewProxy(
		context.Background(),
		pgClient,
		middlewares.HexDumpPacket,
	).RunWithReader(serverReader)

	<-clientRouter.Done()
	<-serverRouter.Done()
	fmt.Printf("srv err-> %v\n", serverRouter.Error())
	fmt.Printf("cli err-> %v\n", clientRouter.Error())
	fmt.Println("END CONNECTION!!")
}
