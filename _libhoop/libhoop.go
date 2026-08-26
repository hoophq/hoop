package libhoop

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"libhoop/agent/mcpadapter"
	"libhoop/aianalyzer"
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
func (p *noopProxy) FlushMetrics(client io.Writer) error { return nil }
func (p *noopProxy) Write(data []byte) (int, error)      { return len(data), nil }
func (p *noopProxy) Done() <-chan struct{}               { return nil }
func (p *noopProxy) Close() error                        { return nil }

func (c *core) MySQL() (Proxy, error)    { return &noopProxy{connectionType: "mysql"}, nil }
func (c *core) MSSQL() (Proxy, error)    { return &noopProxy{connectionType: "mssql"}, nil }
func (c *core) MongoDB() (Proxy, error)  { return &noopProxy{connectionType: "mongodb"}, nil }
func (c *core) Postgres() (Proxy, error) { return &noopProxy{connectionType: "postgres"}, nil }
func (c *core) Oracle() (Proxy, error)   { return &noopProxy{connectionType: "oracle"}, nil }

func (c *core) SSM() (Proxy, error) {
	return &noopProxy{connectionType: "aws-ssm"}, nil
}

func NewAdHocExec(rawEnvVarList map[string]any, args []string, payload []byte, stdout, stderr io.WriteCloser, opts map[string]string) (Proxy, error) {
	return &noopProxy{connectionType: "terminal-exec"}, nil
}

func NewAdHocDBExec(driver string, payload []byte, stdout, stderr io.Writer, opts map[string]string) (Proxy, error) {
	return &noopProxy{connectionType: "db-exec"}, nil
}

func NewConsole(rawEnvVarList map[string]any, args []string, stdout io.WriteCloser, opts map[string]string) (Proxy, error) {
	return &noopProxy{connectionType: "terminal-console"}, nil
}

func NewSSHProxy(ctx context.Context, clientW io.Writer, opts map[string]string) (Proxy, error) {
	return &noopProxy{connectionType: "ssh"}, nil
}

func NewHttpProxy(ctx context.Context, clientW io.Writer, analyzer aianalyzer.Analyzer, opts map[string]string) (Proxy, error) {
	return &noopProxy{connectionType: "httpproxy"}, nil
}

// NewMCPProxy is the OSS stub. The returned proxy reports the missing-library
// error on Run, matching every other protocol in this build.
func NewMCPProxy(ctx context.Context, clientW io.Writer, handler http.Handler, onClose func(), opts map[string]string) (Proxy, error) {
	if err := CheckGuardRailEnforcement(opts["guard_rail_rules"], "mcpproxy"); err != nil {
		return nil, err
	}
	return &noopProxy{connectionType: "mcpproxy"}, nil
}

// NewMCPHooks returns zero hooks: an OSS build has no redaction engine, so the
// MCP gateway has nowhere to evaluate guardrails or masking.
//
// Which is exactly why a connection carrying guardrail rules must be refused
// here rather than served with the hooks left nil (DEP-48, ADR-0004). Zero
// hooks are the honest answer only for a connection that configured none;
// returning them for a guarded one hands the gateway a pipeline whose
// GuardInput and Redact stages are absent, and every tool call, tool
// description and result then flows unexamined with nothing logged.
func NewMCPHooks(opts map[string]string) (mcpadapter.Hooks, error) {
	if err := CheckGuardRailEnforcement(opts["guard_rail_rules"], "mcpproxy"); err != nil {
		return mcpadapter.Hooks{}, err
	}
	return mcpadapter.Hooks{}, nil
}
