package proxy

import (
	"fmt"
	pbterm "github.com/runopsio/hoop/common/terminal"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"

	"github.com/creack/pty"
	pb "github.com/runopsio/hoop/common/proto"
	"golang.org/x/term"
)

type (
	Terminal struct {
		client   pb.ClientTransport
		oldState *term.State
	}
)

func NewTerminal(client pb.ClientTransport) *Terminal {
	return &Terminal{client: client}
}

// Connect control the current terminal connecting with the remote one
func (t *Terminal) ConnectWithTTY() error {
	info, err := os.Stdin.Stat()
	if err != nil {
		return fmt.Errorf("failed obtaining stdin file description, err=%v", err)
	}
	if info.Mode()&os.ModeCharDevice == 0 || info.Size() > 0 {
		return fmt.Errorf("could not allocate a tty, wrong type of device")
	}
	// Set stdin in raw mode.
	t.oldState, err = term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed connecting terminal, err=%v", err)
	}
	ptty, tty, err := pty.Open()
	if err != nil {
		return fmt.Errorf("failed open a new tty, err=%v", err)
	}

	// Handle pty size.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, pbterm.SIGWINCH)
	// TODO: make resize to propagate remotely!
	go func() {
		for {
			switch <-sig {
			case pbterm.SIGWINCH:
				if err := pty.InheritSize(os.Stdin, ptty); err != nil {
					log.Printf("error resizing pty, err=%v", err)
				}
			}
		}
	}()
	sig <- pbterm.SIGWINCH

	go func() {
		sw := pb.NewStreamWriter(t.client, pb.PacketTerminalWriteAgentStdinType, nil)
		_, _ = sw.Write(pbterm.TermEnterKeyStrokeType)
		// TODO: check errors
		_, _ = io.Copy(sw, os.Stdin)
	}()

	go func() {
		<-t.client.StreamContext().Done()
		t.Close()
		signal.Stop(sig)
		close(sig)
		_ = ptty.Close()
		_ = tty.Close()
	}()
	return nil
}

func (t *Terminal) ProcessPacketCloseTerm(pkt *pb.Packet) int {
	t.Close()
	exitCodeStr := string(pkt.Spec[pb.SpecClientExecExitCodeKey])
	exitCode, err := strconv.Atoi(exitCodeStr)
	if exitCodeStr == "" || err != nil {
		// End with a custom exit code, because we don't
		// know what returned from the remote terminal
		exitCode = pbterm.InternalErrorExitCode
	}
	if exitCode != 0 && pkt.Payload != nil {
		os.Stderr.Write(pkt.Payload)
		os.Stderr.Write([]byte{'\n'})
	}
	return exitCode
}

func (t *Terminal) ProcessPacketWriteStdout(pkt *pb.Packet) (int, error) {
	return os.Stdout.Write(pkt.Payload)
}

func (t *Terminal) restoreTerm() {
	if t.oldState == nil {
		return
	}
	if err := term.Restore(int(os.Stdin.Fd()), t.oldState); err != nil {
		fmt.Printf("failed restoring terminal, err=%v\n", err)
	}
}

func (t *Terminal) Close() {
	t.restoreTerm()
	_, _ = t.client.Close()
}
