package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/briandowns/spinner"
	"github.com/creack/pty"
	"github.com/runopsio/hoop/client/grpc"
	pbexec "github.com/runopsio/hoop/common/exec"
	pb "github.com/runopsio/hoop/common/proto"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var execCmd = &cobra.Command{
	Use:          "exec CONNECTION",
	Short:        "Execute a command or start a interactive shell",
	SilenceUsage: false,
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) < 1 {
			cmd.Usage()
			os.Exit(1)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		loader := spinner.New(spinner.CharSets[78], 70*time.Millisecond)
		loader.Color("yellow")
		loader.Start()
		loader.Suffix = " connecting to gateway..."

		client, err := grpc.ConnectGrpc(args[0], pb.ProtocoTerminalType)
		defer loader.Stop()
		if err != nil {
			log.Fatal(err)
			return
		}
		loader.Suffix = fmt.Sprintf(" connecting on %v", args[0])
		runExec(client.Stream, args, loader, func() {
			_ = client.Close()
		})
	},
}

func init() {
	execCmd.Flags().BoolVarP(&ttyFlag, "tty", "t", false, "Stdin is a TTY")
	rootCmd.AddCommand(execCmd)
}

func runExec(stream pb.Transport_ConnectClient, args []string, loader *spinner.Spinner, cleanUpFn func()) error {
	loader.Color("green")
	info, err := os.Stdin.Stat()
	if err != nil {
		panic(err)
	}
	spec := map[string][]byte{}
	if len(args) > 1 {
		encArgs, err := pb.GobEncode(args[1:])
		if err != nil {
			log.Fatalf("failed encoding args, err=%v", err)
		}
		spec[string(pb.SpecClientExecArgsKey)] = encArgs
	}

	var output []byte
	if info.Mode()&os.ModeCharDevice == 0 || info.Size() > 0 {
		if ttyFlag {
			loader.Stop()
			log.Fatalf("could not pass stdin when tty is enabled.")
		}
		stdinPipe := os.NewFile(uintptr(syscall.Stdin), "/dev/stdin")
		reader := bufio.NewReader(stdinPipe)
		for {
			input, err := reader.ReadByte()
			if err != nil && err == io.EOF {
				break
			}
			output = append(output, input)
		}
		_ = stdinPipe.Close()
		_, _ = pb.NewStreamWriter(stream.Send, pb.PacketExecRunProcType, spec).
			Write([]byte(string(output)))
		loader.Suffix = " executing command"
	}
	ptty, tty, err := pty.Open()
	if err != nil {
		loader.Stop()
		log.Fatal(err)
	}
	defer ptty.Close()
	defer tty.Close()

	// Set stdin in raw mode.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		loader.Stop()
		log.Fatal(err)
	}

	// Handle pty size.
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, pbexec.SIGWINCH, syscall.SIGABRT, syscall.SIGTERM, syscall.SIGINT)
	// TODO: make resize to propagate remotely!
	go func() {
		for {
			switch <-ch {
			case pbexec.SIGWINCH:
				if err := pty.InheritSize(os.Stdin, ptty); err != nil {
					log.Printf("error resizing pty, err=%v", err)
				}
			case syscall.SIGABRT, syscall.SIGTERM:
				loader.Stop()
				go func() {
					_ = term.Restore(int(os.Stdin.Fd()), oldState)
					os.Exit(1)
				}()
			case syscall.SIGINT:
				loader.Stop()
				// TODO: check errors
				_ = stream.Send(&pb.Packet{Type: pb.PacketExecCloseTermType.String()})
				// give some time to return all the data after the interrupt
				time.Sleep(time.Second * 4)
				cleanUpFn()
				os.Exit(130) // ctrl+c exit code
			}
		}
	}()
	ch <- pbexec.SIGWINCH
	defer func() { signal.Stop(ch); close(ch) }() // Cleanup signals when done.

	switch {
	case ttyFlag:
		// Copy stdin to the pty and the pty to stdout.
		// NOTE: The goroutine will keep reading until the next keystroke before returning.
		go func() {
			sw := pb.NewStreamWriter(stream.Send, pb.PacketExecWriteAgentStdinType, spec)
			if loader.Enabled() {
				_, _ = sw.Write(pbexec.TermEnterKeyStrokeType)
			}
			// TODO: check errors
			_, _ = io.Copy(sw, os.Stdin)
		}()
	case len(output) == 0:
		loader.Suffix = " executing command"
		_, _ = pb.NewStreamWriter(stream.Send, pb.PacketExecRunProcType, spec).Write(nil)
		_ = term.Restore(int(os.Stdin.Fd()), oldState)
	}

	for {
		pkt, err := stream.Recv()
		if err == io.EOF {
			_ = term.Restore(int(os.Stdin.Fd()), oldState)
			_, _ = os.Stdout.Write(pbexec.TermEnterKeyStrokeType)
			break
		}
		if err != nil {
			_ = term.Restore(int(os.Stdin.Fd()), oldState)
			os.Stdout.Write(pbexec.TermEnterKeyStrokeType)
			log.Fatalf("closing client proxy, err=%v", err)
		}
		switch pb.PacketType(pkt.Type) {
		case pb.PacketExecCloseTermType:
			loader.Stop()
			_ = term.Restore(int(os.Stdin.Fd()), oldState)
			exitCodeStr := string(pkt.Spec[pb.SpecClientExecExitCodeKey])
			exitCode, err := strconv.Atoi(exitCodeStr)
			cleanUpFn()
			if exitCodeStr == "" || err != nil {
				// End with a custom exit code, because we don't
				// know what returned from the remote terminal
				exitCode = pbexec.InternalErrorExitCode
			}
			if exitCode != 0 && pkt.Payload != nil {
				os.Stderr.Write(pkt.Payload)
				os.Stderr.Write([]byte{'\n'})
			}
			os.Exit(exitCode)
		case pb.PacketExecClientWriteStdoutType:
			// 13,10 == \r\n
			if loader.Active() && !bytes.Equal(pkt.Payload, []byte{13, 10}) {
				loader.Stop()
			}
			// TODO: implement write from stderr also!
			_, _ = os.Stdout.Write(pkt.Payload)
		}
	}

	return nil
}
