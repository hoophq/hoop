package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/runopsio/hoop/agent/mongoproxy"
	"github.com/runopsio/hoop/common/log"
	"go.mongodb.org/mongo-driver/x/mongo/driver/connstring"
)

const proxyListenAddr = "0.0.0.0:27017"

// go run main.go 'mongodb://root:1a2b3c4d@<remote-host>:27017'
func main() {
	Serve()
}

func MongoRawConn(addr string) (net.Conn, error) {
	serverConn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("failed dialing server: %s", err)
	}
	fmt.Printf("tcp connection stablished with mongodb server %v. address=%v, local-addr=%v\n",
		addr,
		serverConn.LocalAddr(),
		serverConn.RemoteAddr())
	return serverConn, nil
}

func Serve() {
	if len(os.Args) < 2 {
		fmt.Println("usage: ./proxy.go server:host")
		os.Exit(1)
	}
	connURL, err := connstring.ParseAndValidate(os.Args[1])
	if err != nil {
		panic(err)
	}

	lis, err := net.Listen("tcp4", proxyListenAddr)
	if err != nil {
		panic(err)
	}
	fmt.Printf("serving incoming connections %v\n", lis.Addr().String())
	connID := 1
	for {
		clientConn, err := lis.Accept()
		if err != nil {
			fmt.Printf("listener accept err: %s\n", err)
			time.Sleep(time.Second * 5)
			continue
		}
		serverConn, err := MongoRawConn(connURL.Hosts[0])
		if err != nil {
			panic(err)
		}
		go serveConn(connURL, connID, clientConn, serverConn)
		connID++
	}
}

func serveConn(connURL *connstring.ConnString, connID int, clientConn, serverConn net.Conn) {
	defer clientConn.Close()
	defer serverConn.Close()

	ctx := context.WithValue(context.Background(), mongoproxy.ConnIDContextKey, connID)
	srv := mongoproxy.New(ctx, connURL, serverConn, clientConn)
	srv.Run(nil)
	go func() {
		defer srv.Close()
		// copy from client and write to server proxy
		written, err := io.Copy(srv, clientConn)
		if err != nil && err != io.EOF {
			log.Warnf("failed copying, written=%v, err=%v", written, err)
			srv.Close()
			return
		}
	}()
	<-srv.Done()
}
