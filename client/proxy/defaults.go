package proxy

import (
	"fmt"
	"io"
	"strings"
)

type Closer interface {
	io.Closer
	CloseTCPConnection(connectionID string)
}

const (
	defaultMongoDBPort   = "27018"
	defaultMSSQLPort     = "1444"
	defaultMySQLPort     = "3307"
	defaultPostgresPort  = "5433"
	defaultTCPPort       = "8999"
	defaultSSHPort       = "2222"
	defaultHttpProxyPort = "8081"
)

var defaultListenAddrValue string

func defaultListenAddr(port string) string {
	if defaultListenAddrValue == "" {
		return fmt.Sprintf("127.0.0.1:%s", port)
	}
	return fmt.Sprintf("%s:%s", defaultListenAddrValue, port)
}

type Host struct {
	Port string
	Host string
}

func getListenAddr(listenAddr string) Host {
	host, port, _ := strings.Cut(listenAddr, ":")
	return Host{Host: host, Port: port}
}
