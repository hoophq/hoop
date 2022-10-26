package cmd

type ConnectFlags struct {
	token         string
	serverAddress string
	proxyPort     string
}

var connectFlags = ConnectFlags{}
