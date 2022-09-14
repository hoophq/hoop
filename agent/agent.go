package main

import (
	"context"
	pb "github.com/runopsio/hoop/proto"
	"log"
	"time"
)

type (
	agent struct {
		stream      pb.Transport_ConnectClient
		ctx         context.Context
		closeSignal chan bool
	}
)

func (a *agent) listen() {
	go func() {
		for {
			proto := &pb.Packet{
				Component: pb.PacketAgentComponent,
				Type:      pb.PacketKeepAliveType,
			}
			log.Println("sending keep alive command")
			if err := a.stream.Send(proto); err != nil {
				if err != nil {
					break
				}
				log.Printf("failed sending keep alive command, err=%v", err)
			}
			time.Sleep(time.Second * 10)
		}
	}()

	for {
		msg, err := a.stream.Recv()
		if err != nil {
			log.Printf("%s", err.Error())
			close(a.closeSignal)
			return
		}

		go a.processRequest(msg)
	}
}

func (a *agent) processRequest(packet *pb.Packet) {
	log.Printf("receive request type [%s] from component [%s] and client_id [%s] and payload [%s]",
		packet.Type, packet.Component, packet.Spec["client_id"], string(packet.Payload))

	switch t := packet.Type; t {

	case pb.PacketKeepAliveType:
		return

	case pb.PacketDataStreamType:
		packet.Payload = []byte("here is my response")
		packet.Component = pb.PacketAgentComponent

		if err := a.stream.Send(packet); err != nil {
			log.Printf("send error %v", err)
		}
	}
}
