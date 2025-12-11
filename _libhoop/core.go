package libhoop

import (
	"io"

	"github.com/creack/pty"
)

type Proxy interface {
	Run(func(exitCode int, errMsg string))
	Write(data []byte) (int, error)
	Done() <-chan struct{}
	Close() error

	FlushMetrics(client io.Writer) error
}

type Terminal interface {
	ResizeTTY(size *pty.Winsize) error
}

type DBCore interface {
	MySQL() (Proxy, error)
	Postgres() (Proxy, error)
	MSSQL() (Proxy, error)
	MongoDB() (Proxy, error)
}

type License interface {
	Type() string
	Verify() error
}
