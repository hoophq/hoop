//go:build !libhoop

package libhoop

import (
	"context"
	"fmt"
	"io"
)

type core struct{}
type noopProxy struct {
	connectionType string
}

func NewDBCore(ctx context.Context, clientW io.Writer, opts map[string]string) *core {
	return &core{}
}

func (p *noopProxy) Run(onErr func(int, string)) {
	errMsg := fmt.Sprintf("missing protocol hoop library for %v, contact your administrator", p.connectionType)
	onErr(1, errMsg)
}
func (p *noopProxy) Write(data []byte) (int, error) { return len(data), nil }
func (p *noopProxy) Done() <-chan struct{}          { return nil }
func (p *noopProxy) Close() error                   { return nil }

func (c *core) MySQL() (Proxy, error)    { return &noopProxy{connectionType: "mysql"}, nil }
func (c *core) MSSQL() (Proxy, error)    { return &noopProxy{connectionType: "mssql"}, nil }
func (c *core) MongoDB() (Proxy, error)  { return &noopProxy{connectionType: "mongodb"}, nil }
func (c *core) Postgres() (Proxy, error) { return &noopProxy{connectionType: "postgres"}, nil }

func NewAdHocExec(rawEnvVarList map[string]any, args []string, payload []byte, stdout, stderr io.WriteCloser) (Proxy, error) {
	return &noopProxy{connectionType: "terminal-exec"}, nil
}

func NewConsole(rawEnvVarList map[string]any, args []string, stdout io.WriteCloser) (Proxy, error) {
	return &noopProxy{connectionType: "terminal-console"}, nil
}
