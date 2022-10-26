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
	token := os.Getenv("TOKEN")

	client, err := grpc.Connect(os.Getenv("SERVER_ADDRESS"), token, grpc.WithOption("origin", pb.ConnectionOriginAgent))
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
