package cmd

import (
	"bufio"
	"github.com/briandowns/spinner"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/spf13/cobra"
	"os"
	"strings"
	"syscall"
	"time"
)

var inputFilepath string
var inputStdin string

// execCmd represents the exec command
var execCmd = &cobra.Command{
	Use:   "exec",
	Short: "Execute a given input in a remote resource",
	Run: func(cmd *cobra.Command, args []string) {
		runExec(args)
	},
}

func init() {
	execCmd.Flags().StringVarP(&inputFilepath, "file", "f", "", "The path of the file containing the command")
	execCmd.Flags().StringVarP(&inputStdin, "input", "i", "", "The input to be executed remotely")
	rootCmd.AddCommand(execCmd)
}

func runExec(args []string) {
	config := getClientConfig()

	loader := spinner.New(spinner.CharSets[78], 70*time.Millisecond)
	loader.Color("green")
	loader.Start()
	loader.Suffix = "executing input..."

	c := newClientConnect(config, loader, args, pb.ClientVerbExec)

	pkt := &pb.Packet{
		Type: pb.PacketClientGatewayExecType.String(),
		Spec: newClientArgsSpec(c.clientArgs),
	}

	if pkt.Payload == nil && inputFilepath != "" {
		b, err := os.ReadFile(inputFilepath)
		if err != nil {
			c.printErrorAndExit("failed parsing input file [%s], err=%v", inputFilepath, err)
		}
		pkt.Payload = b
	}

	if pkt.Payload == nil && inputStdin != "" {
		pkt.Payload = []byte(inputStdin)
	}

	if pkt.Payload == nil {
		info, err := os.Stdin.Stat()
		if err != nil {
			panic(err)
		}

		if info.Mode()&os.ModeCharDevice == 0 || info.Size() > 0 {
			stdinPipe := os.NewFile(uintptr(syscall.Stdin), "/dev/stdin")
			defer stdinPipe.Close()

			reader := bufio.NewReader(stdinPipe)
			for {
				input, err := reader.ReadByte()
				if err != nil {
					break
				}
				pkt.Payload = append(pkt.Payload, input)
			}
		}
	}

	if len(pkt.Payload) > 0 {
		pkt.Payload = []byte(strings.Trim(string(pkt.Payload), " \n"))
	}

	if err := c.client.Send(pkt); err != nil {
		_, _ = c.client.Close()
		c.printErrorAndExit("failed executing command, err=%v", err)
	}

	loader.Stop()

	for {
		pkt, err := c.client.Recv()
		c.processGracefulExit(err)
		if pkt != nil {
			c.processPacket(pkt)
		}
	}
}
