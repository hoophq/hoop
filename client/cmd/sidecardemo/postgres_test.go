package sidecardemo

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"os"
	"strings"
	"testing"
	"time"
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

// Through the sidecar, psql goes to the loopback listener in plaintext; the
// comparison step goes to the database over TLS verified against the bundle.
func TestPsqlCommands(t *testing.T) {
	t.Setenv("HOME", "/home/demo")
	p := Ports{Listen: "127.0.0.1:18082", API: PGAddr}
	via := PGSteps[0].Psql(p)
	for _, want := range []string{"PGPASSWORD=" + PGPassword, "host=127.0.0.1 port=18082", "user=" + PGUser,
		"dbname=" + PGDatabase, "sslmode=disable", PGSteps[0].SQL} {
		if !strings.Contains(via, want) {
			t.Errorf("sidecar step %q lacks %q", via, want)
		}
	}
	direct := PGSteps[3].Psql(p)
	for _, want := range []string{"host=" + PGHost + " port=5432", "sslmode=verify-full",
		"sslrootcert=/home/demo/.hoop/sidecar/demo/rds-us-east-1-bundle.pem"} {
		if !strings.Contains(direct, want) {
			t.Errorf("direct step %q lacks %q", direct, want)
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
