package agent

import (
	"fmt"
	"log"

	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/version"
)

func Run() {
	fmt.Println(string(version.JSON()))
	client, err := grpc.Connect("x-agt-test-token")
	if err != nil {
		log.Fatal(err)
	}

	ctx := client.StreamContext()
	done := make(chan struct{})
	agt := New(client, done)
	defer agt.Close()

	go agt.Run()
	<-ctx.Done()
	if err := ctx.Err(); err != nil {
		log.Printf("error: %s", err.Error())
	}
	log.Println("Server terminated connection... exiting...")
}
