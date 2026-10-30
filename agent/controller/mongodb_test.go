package controller

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	pb "github.com/hoophq/hoop/common/proto"
)

func TestMongoshArgs(t *testing.T) {
	got := mongoshArgs("/usr/bin/mongosh", []string{legacyMongoShell, "--quiet", "mongodb://u:p@h:27017/"})
	assert.Equal(t, []string{"/usr/bin/mongosh", "--quiet", "mongodb://u:p@h:27017/", "--file", "/dev/stdin"}, got)
}

func TestMongoExecArgsKeepsOtherCommands(t *testing.T) {
	legacy := []string{legacyMongoShell, "--quiet", "$CONNECTION_STRING"}
	for _, tt := range []struct {
		msg    string
		params pb.AgentConnectionParams
		args   []string
	}{
		{"not mongodb", pb.AgentConnectionParams{ConnectionType: "postgres", ClientVerb: pb.ClientVerbPlainExec}, legacy},
		{"user exec", pb.AgentConnectionParams{ConnectionType: "mongodb", ClientVerb: pb.ClientVerbExec}, legacy},
		{"connection command", pb.AgentConnectionParams{ConnectionType: "mongodb", ClientVerb: pb.ClientVerbPlainExec},
			[]string{"mongo", "--quiet", "$CONNECTION_STRING"}},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			assert.Equal(t, tt.args, mongoExecArgs(&tt.params, tt.args))
		})
	}
}

func TestMongoExecArgsFallsBackToMongosh(t *testing.T) {
	if _, err := os.Stat(legacyMongoShell); err == nil {
		t.Skipf("%v exists on this host", legacyMongoShell)
	}
	dir := t.TempDir()
	mongosh := dir + "/mongosh"
	if err := os.WriteFile(mongosh, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	params := &pb.AgentConnectionParams{ConnectionType: "mongodb", ClientVerb: pb.ClientVerbPlainExec}
	args := []string{legacyMongoShell, "--quiet", "$CONNECTION_STRING"}

	t.Setenv("PATH", t.TempDir())
	assert.Equal(t, args, mongoExecArgs(params, args), "no mongosh: keep the legacy shell")

	t.Setenv("PATH", dir)
	assert.Equal(t, []string{mongosh, "--quiet", "$CONNECTION_STRING", "--file", "/dev/stdin"},
		mongoExecArgs(params, args))
}
