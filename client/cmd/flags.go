package cmd

type ConnectFlags struct {
	serverAddress string
	proxyPort     string
}

var connectFlags = ConnectFlags{}
