// Package spiffe links the SPIFFE Workload API into a sidecar binary. It
// registers two things with the daemon from init: the `spiffe` identity
// type, which proves a client leg by validating the JWT-SVID it presents as
// its password, and the `spiffe` credential source, which presents the
// workload's own X509-SVID on the upstream leg.
//
// Neither constructor touches the Workload API. Validate builds them on the
// control plane and on a developer's macOS, where no agent socket exists, so
// the first Verify or Credential dials. One Workload API client per socket
// address is kept for the life of the process; the X509Source behind the
// credential source is opened on first use and kept, so the agent's SVID
// rotation reaches a running lane without a reconnect.
package spiffe

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/spiffe/go-spiffe/v2/svid/x509svid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
)

// IdentityType is the `identity.type` value and SourceName the pool
// `source` value this package registers.
const (
	IdentityType = "spiffe"
	SourceName   = "spiffe"
)

// DefaultSocket is the Workload API address when neither the config nor
// SPIFFE_ENDPOINT_SOCKET names one: where the SPIRE agent helm chart and
// the spiffe-csi driver put it.
const DefaultSocket = "unix:///run/spire/sockets/agent.sock"

// socketEnv is the variable the SPIFFE Workload API specification names
// for the endpoint; go-spiffe reads it when no address is given.
const socketEnv = "SPIFFE_ENDPOINT_SOCKET"

// callTimeout bounds every Workload API call. A login waits for it; an
// agent that does not answer must fail the login, not hold the client.
const callTimeout = 10 * time.Second

func init() {
	daemon.RegisterIdentitySource(IdentityType, buildVerifier)
	daemon.RegisterCredentialSource(SourceName, buildSource)
}

// socketAddr resolves the Workload API address: the configured one, else
// the environment, else the default.
func socketAddr(configured string) string {
	if configured != "" {
		return configured
	}
	if env := os.Getenv(socketEnv); env != "" {
		return env
	}
	return DefaultSocket
}

// conn is the process-wide state for one Workload API address: the client
// both the verifier and the source call through, and the X509Source the
// source opens on first use. A lane reload rebuilds the verifier and the
// source; both find this conn again instead of dialing a second time.
type conn struct {
	addr string

	mu     sync.Mutex
	client *workloadapi.Client
	source *workloadapi.X509Source
	svids  svidList
}

var (
	connsMu sync.Mutex
	conns   = map[string]*conn{}
)

func connFor(addr string) *conn {
	connsMu.Lock()
	defer connsMu.Unlock()
	c, ok := conns[addr]
	if !ok {
		c = &conn{addr: addr}
		conns[addr] = c
	}
	return c
}

// workloadClient returns the client, dialing on first use. grpc dials in
// the background, so this returns an error only for a malformed address;
// an unreachable agent surfaces on the first call.
func (c *conn) workloadClient(ctx context.Context) (*workloadapi.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != nil {
		return c.client, nil
	}
	client, err := workloadapi.New(ctx, workloadapi.WithAddr(c.addr))
	if err != nil {
		return nil, fmt.Errorf("spiffe: workload api %s: %w", c.addr, err)
	}
	c.client = client
	return client, nil
}

// x509SVIDs returns the SVIDs the agent currently issues this workload,
// opening the X509Source on first use. The source waits for the agent's
// first answer, so the open is bounded by ctx; a failed open leaves
// nothing behind and the next call tries again.
func (c *conn) x509SVIDs(ctx context.Context) ([]*x509svid.SVID, error) {
	client, err := c.workloadClient(ctx)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.source == nil {
		source, err := workloadapi.NewX509Source(ctx,
			workloadapi.WithClient(client),
			workloadapi.WithDefaultX509SVIDPicker(c.svids.pick))
		if err != nil {
			return nil, fmt.Errorf("spiffe: x509 source %s: %w", c.addr, err)
		}
		c.source = source
	}
	svids := c.svids.get()
	if len(svids) == 0 {
		return nil, errors.New("spiffe: the workload api issued no x509-svid")
	}
	return svids, nil
}

// svidList is the X509Source's picker and the list it saw. The source
// exposes one SVID chosen by the picker; a pool entry chooses by SPIFFE ID,
// so the picker keeps the whole list the agent sent and the source reads it
// from here. The source calls pick on every update, so the list follows
// rotation.
type svidList struct {
	mu    sync.RWMutex
	svids []*x509svid.SVID
}

func (l *svidList) pick(svids []*x509svid.SVID) *x509svid.SVID {
	l.mu.Lock()
	l.svids = svids
	l.mu.Unlock()
	if len(svids) == 0 {
		return nil
	}
	return svids[0]
}

func (l *svidList) get() []*x509svid.SVID {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.svids
}
