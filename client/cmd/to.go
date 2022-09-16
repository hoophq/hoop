package cmd

import (
	"bufio"
	"fmt"
	"github.com/briandowns/spinner"
	"github.com/runopsio/hoop/client/grpc"
	pb "github.com/runopsio/hoop/proto"
	"github.com/spf13/cobra"
	"log"
	"os"
	"time"
)

// toCmd represents the to command
var toCmd = &cobra.Command{
	Use:   "to",
	Short: "Specify a connection to hoop to",
	Long: `Provide a connection name.

If the connection is valid, and there is an started agent 
associated with it, you will connect if authenticated.`,

	Run: func(cmd *cobra.Command, args []string) {
		loader := spinner.New(spinner.CharSets[78], 70*time.Millisecond)
		loader.Color("yellow")
		loader.Start()
		loader.Suffix = " connecting to gateway..."

		time.Sleep(time.Second * 1)
		client, err := grpc.ConnectGrpc(args[0])
		if err != nil {
			loader.Stop()
			return
		}

		loader.Stop()

		go listen(client)
		go client.WaitCloseSignal()
		go client.StartKeepAlive()
		//go sendDemoMessages(client) // remove later

		reader := bufio.NewReader(os.Stdin)
		fmt.Print("> ")
		for {
			text, _ := reader.ReadString('\n')

			client.Stream.Send(&pb.Packet{
				Component: pb.PacketClientComponent,
				Type:      pb.PacketDataStreamType,
				Spec:      nil,
				Payload:   []byte(text),
			})
		}
	},
}

func init() {
	rootCmd.AddCommand(toCmd)
}

func listen(c *grpc.Client) {

	for {
		msg, err := c.Stream.Recv()
		if err != nil {
			log.Printf("%s", err.Error())
			close(c.CloseSignal)
			return
		}

		go processResponse(c, msg)
	}
}

func processResponse(c *grpc.Client, packet *pb.Packet) {
	//log.Printf("receive response type [%s] from component [%s] and payload [%s]",
	//	packet.Type, packet.Component, string(packet.Payload))

	switch t := packet.Type; t {

	case pb.PacketKeepAliveType:
		return

	case pb.PacketDataStreamType:
		fmt.Print(string(packet.Payload))
		fmt.Print("> ")
	}
}

func sendDemoMessages(client *grpc.Client) {
	for i := 0; i < 3; i++ {
		client.Stream.Send(&pb.Packet{
			Component: pb.PacketClientComponent,
			Type:      pb.PacketDataStreamType,
			Spec:      nil,
			Payload:   []byte(fmt.Sprintf("please process my request id [%d]", i)),
		})
	}
}
