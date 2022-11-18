package cmd

import (
	"github.com/briandowns/spinner"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/spf13/cobra"
	"time"
)

// execCmd represents the exec command
var execCmd = &cobra.Command{
	Use:   "exec",
	Short: "Execute a command on a remote resource",
	Run: func(cmd *cobra.Command, args []string) {
		runExec(args)
	},
}

func init() {
	execCmd.Flags().StringVarP(&connectFlags.proxyPort, "file", "f", "", "The path of the file containing the command")
	rootCmd.AddCommand(execCmd)
}

func runExec(args []string) {
	config := clientLogin()

	loader := spinner.New(spinner.CharSets[78], 70*time.Millisecond)
	loader.Color("green")
	loader.Start()
	loader.Suffix = "exec'ing command..."

	c, spec := connectionClient(config, loader, args)

	if err := c.client.Send(&pb.Packet{
		Type: pb.PacketClientGatewayExecType.String(),
		Spec: spec,
	}); err != nil {
		_, _ = c.client.Close()
		c.printErrorAndExit("failed connecting to gateway, err=%v", err)
	}

	for {
		pkt, err := c.client.Recv()
		c.processGracefulExit(err)
		if pkt != nil {
			c.processPacket(pkt)
		}
	}
}
