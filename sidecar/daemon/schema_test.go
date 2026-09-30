package daemon

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flag"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

var updateSchema = flag.Bool("update", false, "rewrite schema.json")

// The control plane UI renders the listener form from schema.json, so a
// struct change that is not regenerated would not reach it.
func TestListenerSchemaIsCurrent(t *testing.T) {
	got, err := ListenerSchema()
	if err != nil {
		t.Fatalf("ListenerSchema: %v", err)
	}
	if *updateSchema {
		if err := os.WriteFile("schema.json", got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile("schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("schema.json is stale; run: go test ./daemon -run TestListenerSchemaIsCurrent -update")
	}
}

func TestListenerSchemaRefusesAnUntaggedField(t *testing.T) {
	type block struct {
		Tagged   string `json:"tagged" label:"Tagged"`
		Untagged string `json:"untagged"`
	}
	_, err := schemaFields(reflect.TypeFor[block](), "listeners.x", Protocols(), nil)
	if err == nil || !strings.Contains(err.Error(), "listeners.x.untagged") {
		t.Fatalf("an untagged field must be refused by name, got %v", err)
	}
}

// Each listener field the form renders is set on a minimal lane of every
// protocol, and Validate must accept it exactly where its protocols tag says.
// A tag that drifts from Validate would offer a field the sidecar refuses.
func TestProtocolsTagsMatchValidate(t *testing.T) {
	cert, key := tlsKeypairFiles(t)
	mysqlKey := rsaKeyFile(t)
	hostKey, trustedCA := sshKeys(t)
	sshBlock := map[string]any{"host_key": hostKey, "trusted_ca": trustedCA}

	fixtures := map[string]func(l map[string]any){
		"network": func(l map[string]any) {
			l["network"] = "unix"
			l["listen"] = filepath.Join(t.TempDir(), "l.sock")
		},
		"upstream":        func(l map[string]any) { l["upstream"] = "h:1" },
		"upstream_tls":    func(l map[string]any) { l["upstream_tls"] = map[string]any{} },
		"downstream_tls":  func(l map[string]any) { l["downstream_tls"] = map[string]any{"cert_file": cert, "key_file": key} },
		"identity_header": func(l map[string]any) { l["identity_header"] = "x-user" },
		"idle_timeout_sec": func(l map[string]any) {
			l["idle_timeout_sec"] = 30
		},
		"max_conns": func(l map[string]any) { l["max_conns"] = 5 },
		"mysql_auth_key_file": func(l map[string]any) {
			l["upstream_tls"] = map[string]any{}
			l["mysql_auth_key_file"] = mysqlKey
		},
		"http":       func(l map[string]any) { l["http"] = map[string]any{} },
		"clickhouse": func(l map[string]any) { l["clickhouse"] = map[string]any{} },
		"grpc":       func(l map[string]any) { l["grpc"] = map[string]any{} },
		"spanner":    func(l map[string]any) { l["spanner"] = map[string]any{} },
		"ssh":        func(l map[string]any) { l["ssh"] = sshBlock },
	}
	baseline := func(protocol string) map[string]any {
		l := map[string]any{"name": "l", "protocol": protocol, "listen": "127.0.0.1:1"}
		if protocol == "ssh" {
			l["ssh"] = sshBlock
		} else {
			l["upstream"] = "h:1"
		}
		return l
	}
	load := func(l map[string]any) error {
		raw, err := json.Marshal(map[string]any{"listeners": []any{l}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = LoadConfigBytes(raw)
		return err
	}

	for _, p := range Protocols() {
		if err := load(baseline(p)); err != nil {
			t.Fatalf("the %s baseline must load: %v", p, err)
		}
	}
	for _, f := range jsonFields(reflect.TypeFor[ListenerConfig]()) {
		if slices.Contains(tagList(f.tag, "ui"), "-") || slices.Contains([]string{"name", "protocol", "listen"}, f.name) {
			continue
		}
		set, ok := fixtures[f.name]
		if !ok {
			t.Errorf("listeners.%s has no fixture here, so its protocols tag is unchecked", f.name)
			continue
		}
		for _, p := range Protocols() {
			l := baseline(p)
			set(l)
			err := load(l)
			if want := tagAllows(f.tag, p); want != (err == nil) {
				t.Errorf("listeners.%s on %s: tag allows=%v, Validate error=%v", f.name, p, want, err)
			}
		}
	}
}

func tagAllows(tag reflect.StructTag, protocol string) bool {
	ps := tagList(tag, "protocols")
	if len(ps) == 0 {
		return true
	}
	if strings.HasPrefix(ps[0], "!") {
		ps[0] = ps[0][1:]
		return !slices.Contains(ps, protocol)
	}
	return slices.Contains(ps, protocol)
}

func tlsKeypairFiles(t *testing.T) (cert, key string) {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "sidecar"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cert, key = filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	writePEM(t, cert, "CERTIFICATE", der)
	writePEM(t, key, "PRIVATE KEY", keyDER)
	return cert, key
}

func rsaKeyFile(t *testing.T) string {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "mysql-auth.pem")
	writePEM(t, path, "PRIVATE KEY", der)
	return path
}

func writePEM(t *testing.T, path, kind string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}
