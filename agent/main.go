package agent

import (
	"fmt"
	"github.com/google/uuid"
	pb "github.com/runopsio/hoop/common/proto"
	"log"
	"os"

	"github.com/runopsio/hoop/common/grpc"
	"github.com/runopsio/hoop/common/version"
)

func Run() {
	fmt.Println(string(version.JSON()))

	svrAddr := os.Getenv("SERVER_ADDRESS")
	token := os.Getenv("TOKEN")
	if token == "" {
		token = "x-agt-" + uuid.NewString()
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
