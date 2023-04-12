package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	agentmysql "github.com/runopsio/hoop/agent/mysql"
	authmiddleware "github.com/runopsio/hoop/agent/mysql/middleware/auth"
	"github.com/runopsio/hoop/common/log"
)

const (
	proxyListenAddr = "0.0.0.0:3307"
	mysqlUsername   = "root"
	mysqlPassword   = "1a2b3c4d"
)

// server: go run ./standalone.go mysql-host:3306
// client: mysql -h 0 --port 3307
func main() {
	Serve()
}

func mysqlRawConn(addr string) (net.Conn, error) {
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
		fmt.Println("usage: ./standalone.go server:host")
		os.Exit(1)
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
		serverConn, err := mysqlRawConn(os.Args[1])
		if err != nil {
			fmt.Println(err)
		}
		go serveConn(clientConn, serverConn)
	}
}

// mysql -h 0 --port 3307 -D runopsdev -u root -p1a2b3c4d --ssl-mode=DISABLED
func serveConn(client, serverClient net.Conn) {
	defer client.Close()
	defer serverClient.Close()

	srv := agentmysql.NewProxy(
		context.Background(),
		serverClient,
		client,
		authmiddleware.New(mysqlUsername, mysqlPassword).Handler,
	).Run()
	go func() {
		defer srv.Close()
		// copy from client and write to server proxy
		written, err := copyBuffer(srv, client, nil)
		if err != nil {
			log.Errorf("failed copying, written=%v, err=%v", written, err)
			return
		}
		log.Infof("done reading it")
	}()
	<-srv.Done()
	log.Info("done!")
}

// io.copyBuffer
func copyBuffer(dst io.Writer, src io.Reader, buf []byte) (written int64, err error) {
	// If the reader has a WriteTo method, use it to do the copy.
	// Avoids an allocation and a copy.
	if wt, ok := src.(io.WriterTo); ok {
		return wt.WriteTo(dst)
	}
	// Similarly, if the writer has a ReadFrom method, use it to do the copy.
	if rt, ok := dst.(io.ReaderFrom); ok {
		return rt.ReadFrom(src)
	}
	if buf == nil {
		size := 32 * 1024
		if l, ok := src.(*io.LimitedReader); ok && int64(size) > l.N {
			if l.N < 1 {
				size = 1
			} else {
				size = int(l.N)
			}
		}
		buf = make([]byte, size)
	}
	for {
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
