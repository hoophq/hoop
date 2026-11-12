package daemon

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/identity/token"
)

// CallerTokenConfig is the caller_token block on an http or mcp listener: a request
// is served only when its header holds a token listed in the file, and is
// recorded under Name.
//
// It is for a lane on a public URL, where nothing in front authenticates the
// caller: a Claude Managed Agents cloud sandbox, whose vault fills the header
// with the secret at egress. identity/token holds the trust model.
type CallerTokenConfig struct {
	// Header carries the token. Empty is x-hoop-token. Not Authorization:
	// that is the upstream API's own credential, which the lane forwards.
	Header string `json:"header,omitempty" label:"Header" placeholder:"x-hoop-token" help:"The request header carrying the token. The sidecar removes it before the request reaches the upstream."`

	// File is a path on the sidecar host, one token per line. It is read
	// at startup and re-read when it changes, so a token rotates without a
	// restart. The control plane never reads it.
	File string `json:"file,omitempty" label:"Token file" placeholder:"/etc/hoop-inspect/caller-tokens" help:"A path on the sidecar host. One token per line, so a new token can run beside the old one. Re-read when it changes." ui:"required"`

	// Name is the principal every request admitted by a token is recorded
	// under. A token names no person, so the operator says who it stands
	// for.
	Name string `json:"name,omitempty" label:"Caller name" placeholder:"claude-agents" help:"Recorded as the caller of every request a token admits." ui:"required"`
}

// defaultCallerTokenHeader is the header a caller_token block with no header
// reads.
const defaultCallerTokenHeader = "x-hoop-token"

// header is the lowercased header the token is lifted from.
func (c *CallerTokenConfig) header() string {
	if h := strings.ToLower(strings.TrimSpace(c.Header)); h != "" {
		return h
	}
	return defaultCallerTokenHeader
}

// reservedCallerTokenHeaders cannot carry a caller token. The lane removes
// the token header before the upstream, so a header the upstream reads for
// its own credential (authorization, cookie), one HTTP framing reads
// (content-length, transfer-encoding, host, connection) or one the sidecar
// writes itself (via, x-forwarded-*) would be torn out of every request.
var reservedCallerTokenHeaders = []string{
	"authorization", "proxy-authorization", "cookie", "set-cookie",
	"host", "connection", "upgrade", "content-length", "transfer-encoding",
	"te", "trailer", "keep-alive", "proxy-connection",
	"via", "x-forwarded-host", "x-forwarded-proto",
}

// validate checks the block's own fields. It never reads the file: the plane
// validates without it, and a reload re-validates the whole document, which
// must not be refused over a token file the running lane already handles
// (see token.File.Poll). The file is read where the lane is built; see
// openCallerTokens.
func (c *CallerTokenConfig) validate(lane string, l ListenerConfig) []string {
	var problems []string
	h := c.header()
	switch {
	case !isHeaderName(h):
		problems = append(problems, fmt.Sprintf("%s: caller_token.header %q is not a header name", lane, c.Header))
	case slices.Contains(reservedCallerTokenHeaders, h), analyzer.IsClientModeHeader(h):
		problems = append(problems, fmt.Sprintf(
			"%s: caller_token.header %q is a header the upstream, HTTP framing or the sidecar already uses; pick another",
			lane, c.Header))
	case isMCPGateway(l) && (strings.HasPrefix(h, "mcp-") || h == "last-event-id"):
		problems = append(problems, fmt.Sprintf(
			"%s: caller_token.header %q is a header MCP uses; pick another", lane, c.Header))
	case l.HTTP.captures(h):
		// The codec would keep it in the captured headers, and those
		// reach the audit trail, OPA input and the analyzer's prompt.
		problems = append(problems, fmt.Sprintf(
			"%s: http.headers lists %q, the caller_token header, so the token would reach the audit trail, policy and the analyzer; remove it from http.headers",
			lane, h))
	}
	if strings.TrimSpace(c.File) == "" {
		problems = append(problems, fmt.Sprintf("%s: caller_token.file is required: it lists the accepted tokens", lane))
	}
	if strings.TrimSpace(c.Name) == "" {
		problems = append(problems, fmt.Sprintf("%s: caller_token.name is required: it is the caller the audit trail records", lane))
	}
	return problems
}

// isHeaderName reports whether s is an RFC 9110 field name: one or more
// tchar.
func isHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// openCallerTokens reads the token file of every caller_token lane and hands
// it to the lane. An error names the lane and the file, and stops startup
// (Run) or the -validate report (Validate): a lane that cannot know its
// tokens must not serve.
func openCallerTokens(lanes []lane, log *slog.Logger) ([]*token.File, error) {
	var files []*token.File
	for i := range lanes {
		if err := openCallerToken(&lanes[i], log); err != nil {
			return nil, err
		}
		if lanes[i].callerTokens != nil {
			files = append(files, lanes[i].callerTokens)
		}
	}
	return files, nil
}

// openCallerToken is openCallerTokens for one lane: the shape laneRuntime
// needs when a reload adds a listener.
func openCallerToken(ln *lane, log *slog.Logger) error {
	ct := ln.cfg.CallerToken
	if ct == nil {
		return nil
	}
	var flog *slog.Logger
	if log != nil {
		flog = log.With("listener", ln.name)
	}
	f, err := token.Open(ct.File, flog)
	if err != nil {
		return fmt.Errorf("%s: caller_token.file: %w", ln.name, err)
	}
	ln.callerTokens = f
	return nil
}

// mcpCallerCheck is an mcp lane's caller_token. The key is an HMAC of the
// token under a key drawn per lane, so mcpproxy binds a session to the token
// that opened it and holds nothing a token can be tested against.
func mcpCallerCheck(ln lane) (*MCPCallerCheck, error) {
	ct := ln.cfg.CallerToken
	if ct == nil {
		return nil, nil
	}
	if ln.callerTokens == nil {
		return nil, errors.New("caller_token: the token file was not opened")
	}
	r, err := token.NewResolver(ct.Name, ln.callerTokens)
	if err != nil {
		return nil, fmt.Errorf("caller_token: %w", err)
	}
	secret := []byte(rand.Text())
	return &MCPCallerCheck{Header: ct.header(), Resolve: func(ctx context.Context, credential string) (string, string, error) {
		id, err := r.Resolve(ctx, credential)
		if err != nil {
			return "", "", err
		}
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(strings.TrimSpace(credential)))
		return id.Subject, hex.EncodeToString(mac.Sum(nil)), nil
	}}, nil
}

// watchCallerTokens re-reads the token files on the config file's schedule:
// a stat every interval, and a forced read on SIGHUP. It reads the list per
// tick, because a reload adds and removes lanes, and their files with them.
//
// It is its own goroutine rather than part of the reloader, because the
// reloader stops polling under a control plane, and a token file lives on
// the sidecar host whoever serves the config. signal.Notify delivers SIGHUP
// to every registered channel, so the reloader still gets its copy.
func watchCallerTokens(ctx context.Context, log *slog.Logger, files *tokenWatch, every time.Duration) {
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, f := range files.all() {
				f.Poll()
			}
		case <-hup:
			log.Info("SIGHUP received; re-reading the caller token files")
			for _, f := range files.all() {
				f.Reload()
			}
		}
	}
}
