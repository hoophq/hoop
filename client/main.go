package main

import (
	"context"
	"fmt"
	pb "github.com/runopsio/hoop/domain/proto"
	"google.golang.org/grpc"
	"io"
	"log"
	"math/rand"
	"strconv"
	"time"
)

func main() {
	fmt.Println("starting hoop client...")
	rand.Seed(time.Now().Unix())

	// dail server
	conn, err := grpc.Dial(":9090", grpc.WithInsecure())
	if err != nil {
		log.Fatalf("can not connect with server %v", err)
	}

	// create stream
	client := pb.NewTransportClient(conn)
	stream, err := client.Connect(context.Background())
	if err != nil {
		log.Fatalf("openn stream error %v", err)
	}

	var max int32
	ctx := stream.Context()
	done := make(chan bool)

	// first goroutine sends random increasing numbers to stream
	// and closes it after 10 iterations
	go func() {
		for i := 1; i <= 10; i++ {
			req := pb.Packet{
				Component: "client",
				Type:      strconv.Itoa(i),
				Spec:      nil,
				Payload:   nil,
			}
			log.Printf("sending request type [%s] and component [%s]", req.Type, req.Component)
			if err := stream.Send(&req); err != nil {
				log.Fatalf("can not send %v", err)
			}
			time.Sleep(time.Millisecond * 200)
		}
		if err := stream.CloseSend(); err != nil {
			log.Println(err)
		}
	}()

	// second goroutine receives data from stream
	// and saves result in max variable
	//
	// if stream is finished it closes done channel
	go func() {
		for {
			resp, err := stream.Recv()
			if err == io.EOF {
				close(done)
				return
			}
			if err != nil {
				log.Fatalf("can not receive %v", err)
			}
			log.Printf("receive request type [%s] from component [%s]", resp.Type, resp.Component)

		}
	}()

	// third goroutine closes done channel
	// if context is done
	go func() {
		<-ctx.Done()
		if err := ctx.Err(); err != nil {
			log.Println(err)
		}
		close(done)
	}()

	<-done
	log.Printf("finished with max=%d", max)
}
