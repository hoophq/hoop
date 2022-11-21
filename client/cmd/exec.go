package cmd

import (
	"bufio"
	"github.com/briandowns/spinner"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/spf13/cobra"
	"io"
	"os"
	"strings"
	"syscall"
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

	c, spec := newClientConnect(config, loader, args, pb.ClientVerbExec)

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
		info, err := os.Stdin.Stat()
		if err != nil {
			panic(err)
		}

		if info.Mode()&os.ModeCharDevice == 0 || info.Size() > 0 {
			stdinPipe := os.NewFile(uintptr(syscall.Stdin), "/dev/stdin")
			reader := bufio.NewReader(stdinPipe)
			for {
				input, err := reader.ReadByte()
				if err != nil && err == io.EOF {
					break
				}
				pkt.Payload = append(pkt.Payload, input)
			}
			stdinPipe.Close()
		}
	}

	if pkt.Payload == nil {
		c.printErrorAndExit("missing command, please run 'hoop exec help'")
	}

	pkt.Payload = []byte(strings.Trim(string(pkt.Payload), " \n"))

	if err := c.client.Send(pkt); err != nil {
		_, _ = c.client.Close()
		c.printErrorAndExit("failed exec'ing command, err=%v", err)
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
