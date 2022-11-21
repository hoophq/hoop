package cmd

import (
	"fmt"
	"github.com/briandowns/spinner"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/spf13/cobra"
	"os"
	"time"
)

var commandPath string
var command string

// execCmd represents the exec command
var execCmd = &cobra.Command{
	Use:   "exec",
	Short: "Execute a command on a remote resource",
	Run: func(cmd *cobra.Command, args []string) {
		runExec(args)
	},
}

func init() {
	execCmd.Flags().StringVarP(&commandPath, "file", "f", "", "The path of the file containing the command")
	execCmd.Flags().StringVarP(&command, "command", "c", "", "The command to run remotely")
	rootCmd.AddCommand(execCmd)
}

func runExec(args []string) {
	config := getClientConfig()

	loader := spinner.New(spinner.CharSets[78], 70*time.Millisecond)
	loader.Color("green")
	loader.Start()
	loader.Suffix = "exec'ing command..."

	c, spec := newClientConnect(config, loader, args)

	pkt := &pb.Packet{
		Type: pb.PacketClientGatewayExecType.String(),
		Spec: spec,
	}

	if pkt.Payload == nil && commandPath != "" {
		b, err := os.ReadFile(commandPath)
		if err != nil {
			c.printErrorAndExit("failed parsing command file [%s], err=%v", commandPath, err)
		}
		pkt.Payload = b
	}

	if pkt.Payload == nil && command != "" {
		pkt.Payload = []byte(command)
	}

	if pkt.Payload == nil && len(args) == 2 {
		pkt.Payload = []byte(args[1])
	}

	if pkt.Payload == nil {
		var s []string
		if _, err := fmt.Scanf("%s", &s); err != nil {
			c.printErrorAndExit("failed parsing stdin, err=%v", err)
		}
		fmt.Printf(">>>>>>> ARGS >>>>>>> %s\n", args)
		fmt.Printf(">>>>>>> S >>>>>>> %s\n", s)

		//pkt.Payload = []byte(...s)
	}

	if err := c.client.Send(&pb.Packet{
		Type: pb.PacketClientGatewayExecType.String(),
		Spec: spec,
	}); err != nil {
		_, _ = c.client.Close()
		c.printErrorAndExit("failed connecting to gateway, err=%v", err)
	}

	loader.Stop()
	fmt.Printf(">>>>>>>>>>>>>> %d %s\n", len(args), string(pkt.Payload))

	for {
		pkt, err := c.client.Recv()
		c.processGracefulExit(err)
		if pkt != nil {
			c.processPacket(pkt)
		}
	}
}
