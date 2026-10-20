package sidecardemo

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// The bundle is the three RDS us-east-1 roots, and none is near its end:
// a demo that stops verifying its database one day is a demo that breaks
// without a code change.
func TestRDSBundleIsTheThreeLiveRoots(t *testing.T) {
	var subjects []string
	rest := rdsCABundle
	for {
		var b *pem.Block
		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}
		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		if !c.IsCA || c.Subject.String() != c.Issuer.String() {
			t.Errorf("%s is not a self-signed root", c.Subject)
		}
		if time.Until(c.NotAfter) < 5*365*24*time.Hour {
			t.Errorf("%s expires %s: refresh the bundle from the RDS truststore", c.Subject.CommonName, c.NotAfter)
		}
		subjects = append(subjects, c.Subject.CommonName)
	}
	if len(subjects) != 3 {
		t.Fatalf("bundle holds %v, want the RSA2048, RSA4096 and ECC384 roots", subjects)
	}
	if _, err := RDSRoots(); err != nil {
		t.Fatal(err)
	}
}

// EnsureCA writes the bundle where CAPath says, and writing again over an
// intact copy or a damaged one leaves exactly the bundle.
func TestEnsureCAWritesTheBundleOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path, err := EnsureCA()
	if err != nil {
		t.Fatal(err)
	}
	if want, _ := CAPath(); path != want {
		t.Errorf("path = %s, want %s", path, want)
	}
	if err := os.WriteFile(path, []byte("damaged"), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := EnsureCA(); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(b, rdsCABundle) {
		t.Errorf("file at %s is not the bundle (err %v)", path, err)
	}
}

// psqlArgs runs cmd in /bin/sh with psql replaced by a function that prints
// its arguments, and returns them with the PGPASSWORD psql saw. It is the
// shell's own reading of the command a person pastes.
func psqlArgs(t *testing.T, cmd string) (password string, args []string) {
	t.Helper()
	dir := t.TempDir()
	script := `psql() { printf '%s\n' "$PGPASSWORD"; for a in "$@"; do printf '%s\n' "$a"; done; }; ` + cmd
	c := exec.Command("/bin/sh", "-c", script)
	c.Dir = dir
	out, err := c.Output()
	if err != nil {
		t.Fatalf("sh: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(lines) != 4 || lines[2] != "-c" {
		t.Fatalf("psql got %q, want a password, a connection string, -c and the SQL", lines)
	}
	if entries, _ := os.ReadDir(dir); len(entries) > 0 {
		t.Fatalf("the command ran shell code: it wrote %s", entries[0].Name())
	}
	return lines[0], lines[1:]
}

// Through the sidecar, psql goes to the loopback listener in plaintext; the
// comparison step goes to the database over TLS verified against the bundle.
// A home directory with spaces, quotes and backslashes stays one sslrootcert
// value: pgx parses the connection string psql would get and reads the CA.
func TestPsqlCommands(t *testing.T) {
	home := filepath.Join(t.TempDir(), `it's a "home" \x`)
	t.Setenv("HOME", home)
	ca, err := EnsureCA()
	if err != nil {
		t.Fatal(err)
	}
	p := Ports{Listen: "127.0.0.1:18082", API: PGAddr}

	pw, args := psqlArgs(t, PGSteps[0].Psql(p))
	if pw != PGPassword || args[2] != PGSteps[0].SQL {
		t.Errorf("sidecar step: password %q sql %q", pw, args[2])
	}
	cfg, err := pgconn.ParseConfig(args[0])
	if err != nil || cfg.Host != "127.0.0.1" || cfg.Port != 18082 || cfg.User != PGUser ||
		cfg.Database != PGDatabase || cfg.TLSConfig != nil {
		t.Errorf("sidecar step connects to %+v (err %v), want the plaintext loopback listener", cfg, err)
	}

	// Parsing reads the CA at sslrootcert, so it succeeds only when the odd
	// home directory came through as one value.
	_, args = psqlArgs(t, PGSteps[3].Psql(p))
	cfg, err = pgconn.ParseConfig(args[0])
	if err != nil {
		t.Fatalf("direct step connection string %q (CA at %s): %v", args[0], ca, err)
	}
	if tc := cfg.TLSConfig; cfg.Host != PGHost || tc == nil || tc.InsecureSkipVerify || tc.RootCAs == nil || tc.ServerName != PGHost {
		t.Errorf("direct step %q does not verify %s against the bundle", args[0], PGHost)
	}
}

// An address that reaches the copied command cannot run shell code in it,
// even before CheckAddr refuses it at boot.
func TestPsqlKeepsAnAddressLiteral(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	evil := "$(touch PWNED)`touch PWNED2`'\";.example.com:5432"
	_, args := psqlArgs(t, PGSteps[3].Psql(Ports{Listen: PGListenAddr, API: evil}))
	if !strings.Contains(args[0], "$(touch PWNED)`touch PWNED2`") {
		t.Errorf("connection string %q lost the address's text", args[0])
	}
}

func TestCheckAddr(t *testing.T) {
	for _, ok := range []string{PGAddr, "127.0.0.1:5432", "[::1]:5432", "db-1.example.com:6543"} {
		if err := CheckAddr(ok); err != nil {
			t.Errorf("CheckAddr(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"$(touch x).example.com:5432", "host:abc", "host:0", "a b:5432",
		"host", "-x.com:5432", "x..com:5432", ":5432", "host:5432;rm"} {
		if err := CheckAddr(bad); err == nil {
			t.Errorf("CheckAddr(%q) accepted it", bad)
		}
	}
}

// Only the comparison step goes around the sidecar, and only it carries no
// audit event to tick it.
func TestPGStepsShape(t *testing.T) {
	for i, s := range PGSteps {
		if s.SQL == "" || s.Method != "" {
			t.Errorf("step %d (%s) is not a SQL step", i+1, s.Title)
		}
		if s.Direct != (s.SeenKind == "") {
			t.Errorf("step %d (%s): direct %v but SeenKind %q", i+1, s.Title, s.Direct, s.SeenKind)
		}
	}
}
