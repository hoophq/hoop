package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/runopsio/hoop/agent/dlp"
	"github.com/runopsio/hoop/agent/pgproxy"
	"github.com/runopsio/hoop/common/log"
	"github.com/xo/dburl"
)

const proxyListenAddr = "0.0.0.0:5432"

// go run main.go 'postgres://bob:1a2b3c4d@<remote-host>:5432?sslmode=prefer|require|verify-full|disable'
func main() {
	Serve()
}

func PGRawConn(addr string) (net.Conn, error) {
	serverConn, err := net.Dial("tcp4", addr)
	if err != nil {
		return nil, fmt.Errorf("failed dialing server: %s", err)
	}
	fmt.Printf("tcp connection stablished with postgres server. address=%v, local-addr=%v\n",
		serverConn.LocalAddr(),
		serverConn.RemoteAddr())
	return serverConn, nil
}

func Serve() {
	if len(os.Args) < 2 {
		fmt.Println("usage: ./proxy.go server:host")
		os.Exit(1)
	}
	connURL, err := dburl.Parse(os.Args[1])
	if err != nil {
		panic(err)
	}

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
		serverConn, err := PGRawConn(connURL.Host)
		if err != nil {
			panic(err)
		}
		go serveConn(connURL, clientConn, serverConn)
	}
}

func serveConn(connURL *dburl.URL, client, pgServer net.Conn) {
	defer client.Close()
	defer pgServer.Close()

	srv := pgproxy.New(context.Background(), connURL, pgServer, client)

	gcpRawCred := []byte(`{}`)
	dlpClient, err := dlp.NewDLPClient(context.Background(), gcpRawCred)
	if err == nil {
		srv.WithDataLossPrevention(dlpClient, []string{"PHONE_NUMBER", "EMAIL_ADDRESS", "CREDIT_CARD_NUMBER"})
	}
	srv.Run(nil)
	go func() {
		defer srv.Close()
		// copy from client and write to server proxy
		written, err := copyBuffer(srv, client)
		if err != nil && err != io.EOF {
			log.Warnf("failed copying, written=%v, err=%v", written, err)
			srv.Close()
			return
		}
		log.Infof("done reading it")
	}()
	<-srv.Done()
	log.Infof("end connection")
}

func copyBuffer(dst io.Writer, src io.Reader) (written int64, err error) {
	for {
		buf := make([]byte, 32*1024)
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = errors.New("invalid write result")
				}
			}
			written += int64(nw)
			if ew != nil {
				err = ew
				break
			}
		}
		if er != nil {
			if er != io.EOF {
				err = er
			}
			break
		}
	}
	return written, err
}
