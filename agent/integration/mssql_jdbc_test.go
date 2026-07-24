//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hoophq/hoop/agent/integration/testutil"
	clientproxy "github.com/hoophq/hoop/client/proxy"
	pb "github.com/hoophq/hoop/common/proto"
	pbclient "github.com/hoophq/hoop/common/proto/client"
	"google.golang.org/protobuf/proto"
)

const mssqlJDBCGuardRailRules = `{"input_rules":[{"rules":[{"type":"deny_words_list","words":["secret_table"],"pattern_regex":"","message":"blocked by hoop guardrail: secret_table is off limits"}]}],"output_rules":[{"rules":[]}]}`

type agentInjectTransport struct {
	*testutil.MockTransport
	connID     chan string
	connIDOnce sync.Once
}

func (t *agentInjectTransport) Send(packet *pb.Packet) error {
	if connID := string(packet.Spec[pb.SpecClientConnectionID]); connID != "" {
		t.connIDOnce.Do(func() { t.connID <- connID })
	}
	t.Inject(proto.Clone(packet).(*pb.Packet))
	return nil
}

type jdbcMSSQLBridge struct {
	server  *clientproxy.MSSQLServer
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	errOnce sync.Once
	err     error
}

func startJDBCMSSQLBridge(t *testing.T, tr *testutil.MockTransport, demux *testutil.RecvDemux, sessionID string) *jdbcMSSQLBridge {
	t.Helper()

	clientTransport := &agentInjectTransport{
		MockTransport: tr,
		connID:        make(chan string, 1),
	}
	server := clientproxy.NewMSSQLServer("0", clientTransport)
	if err := server.Serve(sessionID); err != nil {
		t.Fatalf("start JDBC MSSQL listener: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	bridge := &jdbcMSSQLBridge{server: server, cancel: cancel}
	var (
		connID  string
		packets <-chan *pb.Packet
	)
	sessionClosed := demux.SessionCloseChan(sessionID)
	bridge.wg.Add(1)
	go func() {
		defer bridge.wg.Done()
		for {
			select {
			case connID = <-clientTransport.connID:
				packets = demux.Channel(connID)
			case <-ctx.Done():
				return
			case <-sessionClosed:
				if reason, ok := demux.SessionCloseReason(sessionID); ok {
					bridge.recordError(fmt.Errorf("agent closed JDBC MSSQL session: %s", reason))
				}
				return
			case packet := <-packets:
				switch packet.Type {
				case pbclient.MSSQLConnectionWrite:
					if _, err := server.PacketWriteClient(connID, packet); err != nil {
						bridge.recordError(fmt.Errorf("write agent packet to JDBC client: %w", err))
						return
					}
				case pbclient.TCPConnectionClose:
					server.CloseTCPConnection(connID)
				}
			}
		}
	}()

	t.Cleanup(func() {
		if err := bridge.Close(); err != nil {
			t.Errorf("JDBC MSSQL bridge: %v", err)
		}
	})
	return bridge
}

func (b *jdbcMSSQLBridge) recordError(err error) {
	b.errOnce.Do(func() { b.err = err })
}

func (b *jdbcMSSQLBridge) Addr() string {
	return b.server.Host().Addr()
}

func (b *jdbcMSSQLBridge) Close() error {
	b.cancel()
	if err := b.server.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		b.recordError(err)
	}
	b.wg.Wait()
	return b.err
}

func runMSSQLJDBCSmoke(t *testing.T, address, database, user, password string) {
	t.Helper()

	classpathFile := os.Getenv("MSSQL_JDBC_CLASSPATH_FILE")
	if classpathFile == "" {
		t.Fatal("MSSQL_JDBC_CLASSPATH_FILE is required; run `make prepare-mssql-jdbc`")
	}
	rawClasspath, err := os.ReadFile(classpathFile)
	if err != nil {
		t.Fatalf("read MSSQL JDBC classpath file %q: %v", classpathFile, err)
	}
	driverClasspath := strings.TrimSpace(string(rawClasspath))
	if driverClasspath == "" {
		t.Fatalf("MSSQL JDBC classpath file %q is empty", classpathFile)
	}
	for _, dependency := range filepath.SplitList(driverClasspath) {
		if _, err := os.Stat(dependency); err != nil {
			t.Fatalf("MSSQL JDBC dependency is unavailable at %q: %v", dependency, err)
		}
	}

	source := filepath.Join("testdata", "mssql-jdbc", "MSSQLGuardrailsSmoke.java")
	classes := t.TempDir()
	compile := exec.Command("javac", "-cp", driverClasspath, "-d", classes, source)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile MSSQL JDBC smoke client: %v\n%s", err, output)
	}

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("split JDBC MSSQL listener address %q: %v", address, err)
	}
	classpath := classes + string(os.PathListSeparator) + driverClasspath
	ctx, cancel := context.WithTimeout(context.Background(), mssqlTestTimeout)
	defer cancel()
	run := exec.CommandContext(ctx, "java", "-cp", classpath, "MSSQLGuardrailsSmoke", host, port, database, user, password)
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("MSSQL JDBC smoke failed: %v\n%s", err, output)
	}
}

// TestMSSQL_JDBCGuardrails exercises the same Microsoft JDBC driver family
// DBeaver uses, through the production TCP client proxy and the real agent. It
// keeps an empty output-rule placeholder in the payload to cover the UI shape
// that previously caused an input-only connection to be refused.
func TestMSSQL_JDBCGuardrails(t *testing.T) {
	mc := testutil.StartMSSQL(t)
	agent, tr := startAgent(t)
	sessionID := testutil.OpenMSSQLSessionWithGuardRails(t, tr, mc, []byte(mssqlJDBCGuardRailRules))
	demux := testutil.StartRecvDemux(t, tr)
	bridge := startJDBCMSSQLBridge(t, tr, demux, sessionID)
	defer shutdownAgent(t, agent, tr)

	runMSSQLJDBCSmoke(t, bridge.Addr(), mc.Database, "noop", "noop")
	if err := bridge.Close(); err != nil {
		t.Fatalf("JDBC MSSQL bridge failed: %v", err)
	}
}
