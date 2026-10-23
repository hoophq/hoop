package sidecardemo

import (
	"bytes"
	"crypto/x509"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The Postgres demo is the database twin of the HTTP demo in demo.go: the
// sidecar fronts hoop's public demo database, a read-only copy of the
// dellstore sample shop on AWS RDS, so a person with no database at hand can
// watch it redact customer emails and refuse a DROP over a real SQL
// connection. Nothing runs locally but the sidecar; the database is remote.
//
// The credentials below are public on purpose. The role can only read, and
// every row in dellstore is invented, so there is nothing to protect but the
// demo itself, and a demo that needs a secret handed out of band is no demo.

// The demo database and the read-only role every demo connects as.
const (
	PGHost     = "demo-pg-db.ch707rnaizjg.us-east-1.rds.amazonaws.com"
	PGPort     = "5432"
	PGUser     = "demoreadonly"
	PGPassword = "dollar-manger-carouse-HEARTED"
	PGDatabase = "dellstore"
)

// PGAddr is the database the demo listener forwards to.
var PGAddr = net.JoinHostPort(PGHost, PGPort)

// PGListenAddr is where the demo listener prefers to accept psql clients. It
// sits just past the HTTP demo's ports (18080/18081), so the two demos share
// one neighbourhood and neither lands on 15432, the port the setup screen
// gives a real Postgres relay. The setup screen moves it when another program
// holds it.
const PGListenAddr = "127.0.0.1:18082"

// PGDefaultPorts are the preferred addresses for the Postgres demo: the
// listener, and the database it fronts.
var PGDefaultPorts = Ports{Listen: PGListenAddr, API: PGAddr}

// rdsCABundle is Amazon RDS's published us-east-1 root bundle
// (https://truststore.pki.rds.amazonaws.com/us-east-1/us-east-1-bundle.pem):
// the RSA2048, RSA4096 and ECC384 G1 roots. RDS certificates chain to these
// and to nothing in a host trust store, so the demo carries them to verify
// the database instead of skipping verification.
//
//go:embed rds-us-east-1-bundle.pem
var rdsCABundle []byte

// RDSRoots is a pool holding only the RDS roots, for a client that connects
// to the demo database directly.
func RDSRoots() (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(rdsCABundle) {
		return nil, errors.New("the bundled Amazon RDS CA holds no certificate")
	}
	return pool, nil
}

// CAPath is where the demo keeps the RDS bundle on this machine, for the
// sidecar's upstream_tls.ca_file and for psql's sslrootcert: beside the
// sidecar's other files, ~/.hoop/sidecar/demo.
func CAPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("the demo database's CA bundle has no place to go: the home directory is unknown (%v)", err)
	}
	return filepath.Join(home, ".hoop", "sidecar", "demo", "rds-us-east-1-bundle.pem"), nil
}

// EnsureCA writes the RDS bundle to CAPath, unless the file there already
// holds exactly it, and returns the path. The sidecar reads the file when it
// boots, so the CLI calls this before booting a Postgres demo config: the
// config then works on every boot, not only on the machine and the day the
// setup screen wrote it. The file is replaced, never edited in place, so a
// sidecar never reads half of it.
func EnsureCA() (string, error) {
	path, err := CAPath()
	if err != nil {
		return "", err
	}
	if b, err := os.ReadFile(path); err == nil && bytes.Equal(b, rdsCABundle) {
		return path, nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("the demo database's CA bundle: %w", err)
	}
	f, err := os.CreateTemp(dir, ".rds-ca-*")
	if err != nil {
		return "", fmt.Errorf("the demo database's CA bundle: %w", err)
	}
	tmp := f.Name()
	_, werr := f.Write(rdsCABundle)
	cerr := f.Close()
	if err := errors.Join(werr, cerr, os.Chmod(tmp, 0o644)); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("the demo database's CA bundle: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("the demo database's CA bundle: %w", err)
	}
	return path, nil
}

// PGSteps is the guided Postgres demo, the SQL twin of Steps: see masking,
// hit a guardrail, run a query that passes, compare without the sidecar. The
// role is read-only, so the third beat is a read the sidecar lets through
// rather than a write.
var PGSteps = []Step{
	{
		Title: "See masking", SQL: "SELECT customerid, firstname, lastname, email FROM customers LIMIT 5;",
		Expect:   "Every email comes back as [REDACTED:EMAIL_ADDRESS].",
		Why:      "The masking rule \"emails\" found each address in the result and rewrote it before the client saw it.",
		SeenKind: "masked", SeenOp: "select",
	},
	{
		Title: "See a guardrail", SQL: "DROP TABLE customers;",
		Expect:   "Refused: the statement never reaches the database.",
		Why:      "The guardrail \"no-destructive-statements\" matched the DROP and the sidecar answered for the database.",
		SeenKind: "violation", SeenOp: "drop",
	},
	{
		Title: "Run a safe query", SQL: "SELECT title, price FROM products ORDER BY price DESC LIMIT 5;",
		Expect:   "The products as they are: nothing in them is sensitive.",
		Why:      "The query matched no guardrail and no mask rule, so it passed through and was recorded in the audit trail like every statement.",
		SeenKind: "statement", SeenOp: "select", SeenTable: "products",
	},
	{
		Title: "Compare: no sidecar", SQL: "SELECT customerid, firstname, lastname, email FROM customers LIMIT 5;", Direct: true,
		Expect: "The real email addresses, because this query went around the sidecar.",
		Why:    "This query went straight to the database. Only traffic sent to the sidecar listener is inspected.",
	},
}

// Psql is the step as a psql command to paste into another terminal. A step
// through the sidecar connects to its loopback listener in plaintext, the way
// the listener serves; a direct step connects to the database itself over
// TLS, verified against the RDS bundle at CAPath.
func (s Step) Psql(p Ports) string {
	target := p.Listen
	ssl := [][2]string{{"sslmode", "disable"}}
	if s.Direct {
		target = p.API
		ssl = [][2]string{{"sslmode", "require"}}
		if ca, err := CAPath(); err == nil {
			ssl = [][2]string{{"sslmode", "verify-full"}, {"sslrootcert", ca}}
		}
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		host, port = target, PGPort
	}
	conn := ConnString(append([][2]string{{"host", host}, {"port", port}, {"user", PGUser}, {"dbname", PGDatabase}}, ssl...)...)
	return "PGPASSWORD=" + shellQuote(PGPassword) + " psql " + shellQuote(conn) + " -c " + shellQuote(s.SQL)
}

// ConnString renders key/value pairs as a libpq keyword/value connection
// string. Each value is quoted the libpq way, so a space, a quote or a
// backslash in it (a home directory in sslrootcert) stays part of the value.
func ConnString(pairs ...[2]string) string {
	parts := make([]string, len(pairs))
	for i, kv := range pairs {
		parts[i] = kv[0] + "=" + pqQuote(kv[1])
	}
	return strings.Join(parts, " ")
}

func pqQuote(v string) string {
	if v != "" && !strings.ContainsAny(v, " \t\n\r\v\f'\\") {
		return v
	}
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v) + "'"
}

// shellQuote quotes s for a POSIX shell. Nothing expands inside single
// quotes, so $(), backticks and spaces in s stay literal text.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// CheckAddr refuses a demo database address that is not a plain host:port: a
// DNS name or an IP, and a port number. The address reaches the commands the
// tour shows and copies, so anything else is refused, not only quoted.
func CheckAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%q is not host:port", addr)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("%q has no valid port", addr)
	}
	if net.ParseIP(host) != nil {
		return nil
	}
	if host == "" || len(host) > 253 {
		return fmt.Errorf("%q has no valid host", addr)
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return fmt.Errorf("%q has no valid host", addr)
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return fmt.Errorf("%q has no valid host", addr)
			}
		}
	}
	return nil
}
