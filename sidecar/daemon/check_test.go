package daemon

import (
	"errors"
	"strings"
	"testing"
)

// The control plane checks a document off the sidecar host, where the files
// it names do not exist. Only those checks, and the listener count, may be
// skipped; everything else must refuse what the sidecar would refuse.
func TestCheckConfigBytesSkipsOnlyWhatTheHostAnswers(t *testing.T) {
	missingFiles := `{"listeners":[
		{"name":"jump","protocol":"ssh","listen":":2222",
		 "ssh":{"host_key":"/nope/host_key","trusted_ca":"/nope/ca.pub"}},
		{"name":"pg","protocol":"postgres","listen":":5432","upstream":"db:5432",
		 "downstream_tls":{"cert_file":"/nope/tls.crt","key_file":"/nope/tls.key"}},
		{"name":"my","protocol":"mysql","listen":":3306","upstream":"db:3306",
		 "upstream_tls":{},"mysql_auth_key_file":"/nope/rsa.pem"}]}`
	if err := CheckConfigBytes([]byte(missingFiles)); err != nil {
		t.Fatalf("files on the sidecar host must not be checked: %v", err)
	}
	if _, err := LoadConfigBytes([]byte(missingFiles)); err == nil {
		t.Fatal("the sidecar itself must still refuse the missing files")
	}
	if err := CheckConfigBytes([]byte(`{}`)); err != nil {
		t.Fatalf("a control-plane document may have no listener yet: %v", err)
	}

	err := CheckConfigBytes([]byte(`{"listeners":[
		{"name":"newdb","protocol":"ssh","listen":"0.0.0.0:1234",
		 "ssh":{"host_key":"/etc/test","trusted_ca":"/etc/trusted_ca",
		        "destinations_allowed":["10.30.10:1234"],
		        "identity":{"subject":"key.id","email":"email","groups":"admion"}}},
		{"name":"pg","protocol":"postgres","listen":":5432","upstream":"db:5432",
		 "downstream_tls":{"cert_file":"/etc/tls.crt"}}]}`))
	if err == nil {
		t.Fatal("invalid values were accepted")
	}
	for _, want := range []string{
		`ssh.destinations_allowed: "10.30.10:1234" is not a network`,
		`ssh.identity.subject names "key.id"`,
		`ssh.identity.email names "email"`,
		`ssh.identity.groups names "admion"`,
		"downstream_tls needs both cert_file and key_file",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "no such file") {
		t.Errorf("a file was opened: %v", err)
	}
	var problems ConfigProblems
	if !errors.As(err, &problems) || len(problems) != 5 {
		t.Fatalf("want the five problems as a list, got %T %v", err, err)
	}
	if !strings.HasPrefix(problems[0], "newdb: ") {
		t.Errorf("a problem must start with its listener: %q", problems[0])
	}
}
