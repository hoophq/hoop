package main

import (
	"github.com/runopsio/hoop/client/cmd"
)

func main() {
	cmd.Execute()

	//done := make(chan bool)
	//
	//client, err := connectGrpc()
	//if err != nil {
	//	log.Printf("exiting...error connecting with server  %v", err)
	//	close(done)
	//	return
	//}
	//
	//client.closeSignal = done
	//
	//go waitCloseSignal(client)
	//go client.listen()
	//
	//go sendDemoMessages(client) // remove later
	//
	//<-done
	//log.Println("Server terminated connection... exiting...")
}
