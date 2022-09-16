/*
Copyright © 2022 NAME HERE <EMAIL ADDRESS>

*/
package cmd

import (
	"github.com/runopsio/hoop/client/grpc"
	pb "github.com/runopsio/hoop/proto"
	"github.com/spf13/cobra"
	"log"
)

// toCmd represents the to command
var toCmd = &cobra.Command{
	Use:   "to",
	Short: "Specify a connection to hoop to",
	Long: `Provide a connection name.

If the connection is valid, and there is an started agent 
associated with it, you will connect if authenticated.`,
	Run: func(cmd *cobra.Command, args []string) {
		client, err := grpc.ConnectGrpc(args[0])
		if err != nil {
			return
		}

		done := client.CloseSignal
		go listen(client)
		go client.StartKeepAlive()
		<-done
	},
}

func init() {
	rootCmd.AddCommand(toCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// toCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// toCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

//func sendDemoMessages(client *client) {
//	for i := 0; i < 3; i++ {
//		client.stream.Send(&pb.Packet{
//			Component: pb.PacketClientComponent,
//			Type:      pb.PacketDataStreamType,
//			Spec:      nil,
//			Payload:   []byte(fmt.Sprintf("please process my request id [%d]", i)),
//		})
//	}
//}

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
	log.Printf("receive response type [%s] from component [%s] and payload [%s]",
		packet.Type, packet.Component, string(packet.Payload))

	switch t := packet.Type; t {

	case pb.PacketKeepAliveType:
		return

	case pb.PacketDataStreamType:
		log.Println("show outcome to final user...")
	}
}
