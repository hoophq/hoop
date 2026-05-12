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

// StartSSH boots an OpenSSH server in a container. Password auth is enabled
// for the test user so password-based credentials match what
// parseConnectionEnvVars expects.
//
// linuxserver/openssh-server takes a few seconds to provision the user; the
// wait strategy polls the TCP port until it accepts connections rather than
// scraping logs, which is more reliable across image versions.
func StartSSH(t T) *SSHContainer {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	const user = "testuser"
	const password = "testpass"

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "lscr.io/linuxserver/openssh-server:latest",
			ExposedPorts: []string{"2222/tcp"},
			Env: map[string]string{
				"PUID":            "1000",
				"PGID":            "1000",
				"TZ":              "Etc/UTC",
				"USER_NAME":       user,
				"USER_PASSWORD":   password,
				"PASSWORD_ACCESS": "true",
				"SUDO_ACCESS":     "false",
			},
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

	host, err := container.Host(ctx)
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
