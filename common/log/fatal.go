package log

import (
	"os"
	"sync"
	"time"

	"go.uber.org/zap/zapcore"
)

// fatalFlushBound caps the run of the OnFatal functions. A function that has
// not returned by then is left behind, and the process exits.
var fatalFlushBound = 5 * time.Second

var (
	fatalMu      sync.Mutex
	fatalFlushes []func()
	fatalOnce    sync.Once
	exitProcess  = os.Exit
)

// OnFatal registers fn to run after a Fatal entry is written and before the
// process exits with status 1. The functions run in registration order, and
// the whole run is cut at fatalFlushBound. With none registered, a Fatal
// entry exits as zap's default hook does.
func OnFatal(fn func()) {
	fatalMu.Lock()
	fatalFlushes = append(fatalFlushes, fn)
	fatalMu.Unlock()
}

// fatalHook replaces zap's WriteThenFatal. A second Fatal, from another
// goroutine or from a flush, waits for the first run, then exits.
type fatalHook struct{}

func (fatalHook) OnWrite(*zapcore.CheckedEntry, []zapcore.Field) {
	fatalOnce.Do(runFatalFlushes)
	exitProcess(1)
}

func runFatalFlushes() {
	fatalMu.Lock()
	fns := append([]func(){}, fatalFlushes...)
	fatalMu.Unlock()
	if len(fns) == 0 {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, fn := range fns {
			runRecovered(fn)
		}
	}()
	select {
	case <-done:
	case <-time.After(fatalFlushBound):
	}
}

// runRecovered keeps a panicking flush from changing the exit status from 1
// to 2, and from stopping the flushes after it.
func runRecovered(fn func()) {
	defer func() { _ = recover() }()
	fn()
}
