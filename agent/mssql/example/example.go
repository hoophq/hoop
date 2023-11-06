package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/runopsio/hoop/agent/mssql"
	"github.com/runopsio/hoop/common/log"
	mssqltypes "github.com/runopsio/hoop/common/mssql/types"
)

const (
	proxyListenAddr = "0.0.0.0:1444"
)

/*
	sqlcmd create mssql --accept-eula --name sql \
		--password-length 10  \
		--password-min-upper 3  \
		--password-min-special 3 \
		--user-database bob \
		--port 1433 \
		--using https://aka.ms/AdventureWorksLT.bak,adventureworks

# get credentials
sqlcmd config cs
# change the password to something more simple
sqlcmd ... -z1a2b3c4d
# run the proxy
go run main.go 'sqlserver://<user>:1a2b3c4d@127.0.0.1:1433?insecure=true'
# connect it without password
sqlcmd -S 127.0.0.1:1444 -Q "SELECT @@VERSION"
*/
func main() {
	Serve()
}

func sqlServerRawConn(addr string) (net.Conn, error) {
	serverConn, err := net.Dial("tcp4", addr)
	if err != nil {
		return nil, fmt.Errorf("failed dialing server: %s", err)
	}
	fmt.Printf("tcp connection stablished with mysql server. address=%v, local-addr=%v\n",
		serverConn.LocalAddr(),
		serverConn.RemoteAddr())
	return serverConn, nil
}

func Serve() {
	if len(os.Args) < 2 {
		fmt.Println("usage: ./proxy.go server:host")
		os.Exit(1)
	}
	connStr, err := url.Parse(os.Args[1])
	if err != nil {
		panic(err)
	}
	lis, err := net.Listen("tcp4", proxyListenAddr)
	if err != nil {
		panic(err)
	}
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
		serverConn, err := sqlServerRawConn(connStr.Host)
		if err != nil {
			panic(err)
		}
		go serveConn(connStr, clientConn, serverConn)
	}
}

func serveConn(connStr *url.URL, client, serverClient net.Conn) {
	defer client.Close()
	defer serverClient.Close()

	sessionID := uuid.NewString()
	fmt.Printf("--------->>> starting proxy session %s <<<---------\n", sessionID)
	srv := mssql.NewProxy(context.Background(), connStr, serverClient, client).Run(nil)
	go func() {
		defer srv.Close()
		// copy from client and write to server proxy
		written, err := copyBuffer(&clientWriter{srv}, client)
		if err != nil && err != io.EOF {
			log.Warnf("failed copying, written=%v, err=%v", written, err)
			srv.Close()
			return
		}
		log.Infof("done reading it")
	}()
	<-srv.Done()
	fmt.Printf("--------->>> end proxy session %s <<<---------\n", sessionID)
}

type clientWriter struct {
	srv mssql.Proxy
}

func (w clientWriter) Write(p []byte) (int, error) {
	pktList, err := mssqltypes.DecodeFull(p, mssqltypes.DefaultPacketSize)
	if err != nil {
		return 0, err
	}
	for _, pkt := range pktList {
		if _, err := w.srv.Write(pkt.Encode()); err != nil {
			return 0, err
		}
	}
	return 0, nil
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
