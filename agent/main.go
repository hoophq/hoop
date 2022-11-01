package agent

import (
	"fmt"
	pb "github.com/runopsio/hoop/common/proto"
	"log"
	"os"

	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/version"
)

func Run() {
	fmt.Println(string(version.JSON()))

	svrAddr := os.Getenv("SERVER_ADDRESS")
	if svrAddr == "" {
		log.Fatal("missing required SERVER_ADDRESS variable")
	}

	token := os.Getenv("TOKEN")
	if token == "" {
		log.Fatal("missing required TOKEN variable")
	}

	client, err := grpc.Connect(svrAddr, token, grpc.WithOption("origin", pb.ConnectionOriginAgent))
	if err != nil {
		log.Fatal(err)
	}

	ctx := client.StreamContext()
	done := make(chan struct{})
	agt := New(client, done)
	defer agt.Close()

	go agt.Run(svrAddr, token)
	<-ctx.Done()
	if err := ctx.Err(); err != nil {
		log.Printf("error: %s", err.Error())
	}
	log.Println("Server terminated connection... exiting...")
}
