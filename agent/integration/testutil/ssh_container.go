//go:build integration

package testutil

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
)

// SSHContainer wraps a linuxserver/openssh-server container for integration
// tests. Credentials are fixed so test code can reference them directly:
// user "testuser" / password "testpass".
type SSHContainer struct {
	Host      string
	Port      string
	User      string
	Password  string
	Container testcontainers.Container
}

// StartSSHWithForwarding boots an OpenSSH server with TCP forwarding
// enabled. linuxserver/openssh-server defaults `AllowTcpForwarding no`,
// which blocks the direct-tcpip channels exercised by port-forward
// tests. This variant runs an exec inside the container after start
// to flip the config and reload sshd.
func StartSSHWithForwarding(t T) *SSHContainer {
	c := startSSHContainer(t, "")
	c.enableTCPForwarding(t)
	return c
}

// StartSSH boots an OpenSSH server in a container. Password auth is enabled
// for the test user so password-based credentials match what
// parseConnectionEnvVars expects.
//
// linuxserver/openssh-server takes a few seconds to provision the user; the
// wait strategy polls the TCP port until it accepts connections rather than
// scraping logs, which is more reliable across image versions.
//
// Use StartSSHWithPublicKey for tests that need pubkey auth.
func StartSSH(t T) *SSHContainer {
	return startSSHContainer(t, "")
}

// StartSSHWithPublicKey boots an OpenSSH server with the given public key
// installed in the test user's authorized_keys, plus password auth still
// enabled as a fallback. Pass the public key in OpenSSH authorized-keys
// format (e.g. "ssh-rsa AAAAB3..."). Used by pubkey-auth tests.
func StartSSHWithPublicKey(t T, publicKey string) *SSHContainer {
	return startSSHContainer(t, publicKey)
}

func startSSHContainer(t T, publicKey string) *SSHContainer {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	const user = "testuser"
	const password = "testpass"

	env := map[string]string{
		"PUID":            "1000",
		"PGID":            "1000",
		"TZ":              "Etc/UTC",
		"USER_NAME":       user,
		"USER_PASSWORD":   password,
		"PASSWORD_ACCESS": "true",
		"SUDO_ACCESS":     "false",
	}
	if publicKey != "" {
		// linuxserver/openssh-server's entrypoint reads PUBLIC_KEY
		// and appends it to the user's authorized_keys file.
		env["PUBLIC_KEY"] = publicKey
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "lscr.io/linuxserver/openssh-server:latest",
			ExposedPorts: []string{"2222/tcp"},
			Env:          env,
			WaitingFor: wait.ForListeningPort("2222/tcp").
				WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("failed to start openssh container: %v", err)
	}

	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	mappedPort, err := container.MappedPort(ctx, "2222/tcp")
	if err != nil {
		t.Fatalf("failed to get mapped ssh port: %v", err)
	}

	host, err := ContainerHost(ctx, container)
	if err != nil {
		t.Fatalf("failed to get ssh container host: %v", err)
	}

	c := &SSHContainer{
		Host:      host,
		Port:      mappedPort.Port(),
		User:      user,
		Password:  password,
		Container: container,
	}

	// linuxserver/openssh-server briefly accepts TCP before sshd is fully
	// initialized — the port is up while user provisioning still runs in
	// the entrypoint. Probe the banner to make sure sshd is actually
	// speaking SSH before returning.
	c.waitForBanner(t)

	return c
}

// waitForBanner dials the SSH port and reads the server identification
// string ("SSH-2.0-..."). Polls until a banner is received or the timeout
// fires.
func (c *SSHContainer) waitForBanner(t T) {
	deadline := time.Now().Add(30 * time.Second)
	addr := net.JoinHostPort(c.Host, c.Port)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 7)
		n, err := conn.Read(buf)
		_ = conn.Close()
		if err == nil && n >= 4 && string(buf[:4]) == "SSH-" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("ssh container at %s never produced an SSH banner within 30s", addr)
}

// ConnString returns a host:port string suitable for direct TCP dialing.
func (c *SSHContainer) ConnString() string {
	return fmt.Sprintf("%s:%s", c.Host, c.Port)
}

// enableTCPForwarding edits the sshd_config inside the container so
// direct-tcpip channels work, then sends a HUP to sshd to pick up
// the change. linuxserver/openssh-server ships with
// `AllowTcpForwarding no` and there's no env var to override it.
//
// Idempotent — calling twice does no harm.
func (c *SSHContainer) enableTCPForwarding(t T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Patch the running sshd config, then reload via SIGHUP. The
	// process pidfile lives at the standard /config/sshd/sshd.pid in
	// linuxserver, but pkill is simpler and works across versions.
	_, _, err := c.Container.Exec(ctx, []string{
		"sh", "-c",
		"sed -i 's/^AllowTcpForwarding no/AllowTcpForwarding yes/' /config/sshd/sshd_config && " +
			"pkill -HUP sshd || true",
	}, tcexec.Multiplexed())
	if err != nil {
		t.Fatalf("ssh-container: enableTCPForwarding exec: %v", err)
	}

	// Settle delay so sshd's HUP reload completes before tests
	// drive port-forward traffic. SIGHUP causes sshd to re-exec
	// itself; a few hundred ms is enough.
	time.Sleep(500 * time.Millisecond)
}

// HTTPTarget represents a small HTTP server container intended as the
// destination for SSH direct-tcpip port-forward tests. The
// ContainerIP and ContainerPort fields point at the in-network
// address reachable from other containers on the docker bridge
// network (which is what the SSH container needs); HostPort exposes
// the same service on the host for sanity-check fetches from the
// test process.
type HTTPTarget struct {
	ContainerIP   string
	ContainerPort string
	HostPort      string
	Container     testcontainers.Container
}

// StartHTTPTarget boots a minimal nginx container that serves a fixed
// response. Used by the port-forward test as the target the SSH
// channel is forwarded to.
func StartHTTPTarget(t T) *HTTPTarget {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "nginx:alpine",
			ExposedPorts: []string{"80/tcp"},
			WaitingFor: wait.ForListeningPort("80/tcp").
				WithStartupTimeout(30 * time.Second),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("failed to start nginx container: %v", err)
	}
	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	port, err := container.MappedPort(ctx, "80/tcp")
	if err != nil {
		t.Fatalf("nginx port: %v", err)
	}

	// Inspect the container to find its IP on the default docker
	// bridge. We need this address — not the host-mapped one — to
	// reach nginx from inside the SSH container (which is the
	// destination of the direct-tcpip forward).
	state, err := container.Inspect(ctx)
	if err != nil {
		t.Fatalf("nginx inspect: %v", err)
	}
	var ip string
	if state != nil && state.NetworkSettings != nil {
		for _, network := range state.NetworkSettings.Networks {
			if network != nil && network.IPAddress.IsValid() {
				ip = network.IPAddress.String()
				break
			}
		}
	}
	if ip == "" {
		t.Fatalf("nginx container has no IP address on any network")
	}

	return &HTTPTarget{
		ContainerIP:   ip,
		ContainerPort: "80",
		HostPort:      port.Port(),
		Container:     container,
	}
}

// HostURL returns the http://host:port the test process can hit
// directly (used to verify nginx is actually running before
// exercising the port-forward path).
func (h *HTTPTarget) HostURL() string {
	return fmt.Sprintf("http://127.0.0.1:%s/", h.HostPort)
}

// EstablishedSSHConnections returns the number of established TCP
// connections to sshd's listening port (2222) inside the container.
// Used by the singleflight test to assert that concurrent first-packets
// for the same (sid, connID) result in exactly one upstream SSH
// connection — if the dedup primitive is broken, this count balloons.
//
// linuxserver/openssh-server is an Alpine-based image with netstat from
// busybox available (no ss). We count lines where the local address
// ends in ":2222" and the state column is ESTABLISHED. tcexec's
// Multiplexed() option flattens the docker stream multiplexing
// (stdout/stderr framing) so the reader yields clean shell output.
func (c *SSHContainer) EstablishedSSHConnections(t T) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, reader, err := c.Container.Exec(ctx,
		[]string{"sh", "-c", "netstat -tn 2>/dev/null"},
		tcexec.Multiplexed(),
	)
	if err != nil {
		t.Fatalf("ssh-container: failed to exec netstat: %v", err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ssh-container: failed reading netstat output: %v", err)
	}

	// busybox netstat output looks like:
	//   Proto Recv-Q Send-Q Local Address Foreign Address State
	//   tcp        0      0 172.17.0.2:2222 172.17.0.1:44814 ESTABLISHED
	//
	// We don't want to depend on column positions because busybox and
	// GNU netstat differ subtly. Count any line that mentions :2222
	// somewhere AND has the ESTABLISHED token. Header line never
	// contains "ESTABLISHED" so it's naturally excluded.
	count := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.Contains(line, ":2222") {
			continue
		}
		if !strings.Contains(line, "ESTABLISHED") {
			continue
		}
		count++
	}

	return count
}

// WaitForEstablishedSSHConnections polls EstablishedSSHConnections until
// it reaches the target count or the timeout fires. Useful in
// concurrency tests where the upstream dial happens asynchronously
// inside libhoop's proxy goroutine and may take a beat to register.
func (c *SSHContainer) WaitForEstablishedSSHConnections(t T, target int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last int
	for time.Now().Before(deadline) {
		last = c.EstablishedSSHConnections(t)
		if last == target {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("ssh-container: connection count did not reach %d within %v (last=%d)",
		target, timeout, last)
}
