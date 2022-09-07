package main

import (
	pb "github.com/runopsio/hoop/domain/proto"
	"github.com/runopsio/hoop/gateway/transport"
	"google.golang.org/grpc"
	"log"
	"net"
)

func startRPCServer() {
	log.Println("Starting gRPC server...")

	listener, err := net.Listen("tcp", ":9090")
	if err != nil {
		panic(err)
	}

	s := grpc.NewServer()
	pb.RegisterTransportServer(s, &transport.Server{})
	if err := s.Serve(listener); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
