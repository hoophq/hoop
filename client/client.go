package main

import (
	"context"
	pb "github.com/runopsio/hoop/proto"
	"log"
	"time"
)

type (
	client struct {
		stream      pb.Transport_ConnectClient
		ctx         context.Context
		closeSignal chan bool
	}
)

func (c *client) listen() {
	go func() {
		for {
			proto := &pb.Packet{
				Component: pb.PacketClientComponent,
				Type:      pb.PacketKeepAliveType,
			}
			log.Println("sending keep alive command")
			if err := c.stream.Send(proto); err != nil {
				if err != nil {
					break
				}
				log.Printf("failed sending keep alive command, err=%v", err)
			}
			time.Sleep(time.Second * 10)
		}
	}()

	for {
		msg, err := c.stream.Recv()
		if err != nil {
			log.Printf("%s", err.Error())
			close(c.closeSignal)
			return
		}

		go c.processResponse(msg)
	}
}

func (c *client) processResponse(packet *pb.Packet) {
	log.Printf("receive response type [%s] from component [%s] and payload [%s]",
		packet.Type, packet.Component, string(packet.Payload))

	switch t := packet.Type; t {

	case pb.PacketKeepAliveType:
		return

	case pb.PacketDataStreamType:
		log.Println("show here the outcome to the final user...")
	}
}
