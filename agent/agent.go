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

func (a *agent) processRequest(packet *pb.Packet) {
	clientId := packet.Spec["client_id"]

	log.Printf("receive request type [%s] from component [%s] and client_id [%s] and payload [%s]",
		packet.Type, packet.Component, clientId, string(packet.Payload))

	switch t := packet.Type; t {

	case pb.PacketKeepAliveType:
		return

	case pb.PacketDataStreamType:
		log.Printf("sending response to client_id [%s]", clientId)

		packet.Payload = []byte("here is my response")
		packet.Component = pb.PacketAgentComponent

		//payload := string(packet.Payload)
		//exec.Command(payload[0], payload[1:])

		if err := a.stream.Send(packet); err != nil {
			log.Printf("send error %v", err)
		}
	}
}

func (a *agent) listen() {
	go a.startKeepAlive()

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

func (a *agent) startKeepAlive() {
	for {
		proto := &pb.Packet{
			Component: pb.PacketAgentComponent,
			Type:      pb.PacketKeepAliveType,
		}
		log.Println("sending keep alive command")
		if err := a.stream.Send(proto); err != nil {
			if err != nil {
				log.Printf("failed sending keep alive command, err=%v", err)
				break
			}
		}
		time.Sleep(pb.DefaultKeepAlive)
	}
}
