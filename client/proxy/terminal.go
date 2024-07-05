package proxy

import (
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/creack/pty"
	pb "github.com/hoophq/hoop/common/proto"
	pbagent "github.com/hoophq/hoop/common/proto/agent"
	pbterm "github.com/hoophq/hoop/common/terminal"
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

	go func() {
		sw := pb.NewStreamWriter(t.client, pbagent.TerminalWriteStdin, nil)
		_, _ = sw.Write(pbterm.TermEnterKeyStrokeType)
		_, _ = io.Copy(sw, os.Stdin)
	}()

	// Handle pty size.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, pbterm.SIGWINCH)
	go func() {
		for range sig {
			size, err := pty.GetsizeFull(os.Stdin)
			if err == nil {
				resizeMsg := fmt.Sprintf("%v,%v,%v,%v", size.Rows, size.Cols, size.X, size.Y)
				_, _ = pb.NewStreamWriter(t.client, pbagent.TerminalResizeTTY, nil).
					Write([]byte(resizeMsg))
			}
		}
	}()
	sig <- pbterm.SIGWINCH

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

func (t *Terminal) CloseTCPConnection(_ string) {}
func (t *Terminal) Close() error {
	t.restoreTerm()
	_, _ = t.client.Close()
	return nil
}
