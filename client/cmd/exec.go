package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/briandowns/spinner"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/spf13/cobra"
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

func parseFlagInputs(c *connect) []byte {
	if inputFilepath != "" && inputStdin != "" {
		c.printErrorAndExit("accept only one option: --file (-f) or --input (-i)")
	}
	switch {
	case inputFilepath != "":
		input, err := os.ReadFile(inputFilepath)
		if err != nil {
			c.printErrorAndExit("failed parsing input file [%s], err=%v", inputFilepath, err)
		}
		return input
	case inputStdin != "":
		return []byte(inputStdin)
	}
	return nil
}

func parseExecInput(c *connect) []byte {
	info, err := os.Stdin.Stat()
	if err != nil {
		c.printErrorAndExit(err.Error())
	}
	var input []byte
	// stdin input
	if info.Mode()&os.ModeCharDevice == 0 || info.Size() > 0 {
		if inputFilepath != "" || inputStdin != "" {
			c.printErrorAndExit("flags not allowed when reading from stdin")
		}
		stdinPipe := os.NewFile(uintptr(syscall.Stdin), "/dev/stdin")
		reader := bufio.NewReader(stdinPipe)
		for {
			stdinInput, err := reader.ReadByte()
			if err != nil && err == io.EOF {
				break
			}
			input = append(input, stdinInput)
		}
		stdinPipe.Close()
	}
	if len(input) > 0 {
		return input
	}
	return parseFlagInputs(c)
}

func runExec(args []string) {
	config := getClientConfig()

	loader := spinner.New(spinner.CharSets[78], 70*time.Millisecond)
	loader.Color("green")
	loader.Start()
	loader.Suffix = " executing input ..."

	c := newClientConnect(config, loader, args, pb.ClientVerbExec)
	pkt := &pb.Packet{
		Type: pb.PacketClientGatewayExecType.String(),
		Spec: newClientArgsSpec(c.clientArgs),
	}
	pkt.Payload = parseExecInput(c)
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
			c.processPacket(pkt, config, loader)
		}
	}
}

func buildReviewUrl(conf *Config, id string, url string) string {
	protocol := "https"
	if strings.HasPrefix(conf.Host, "127.0.0.1") {
		protocol = "http"
	}
	return fmt.Sprintf("%s://%s/plugins/%s/%s", protocol, conf.Host, url, id)
}
