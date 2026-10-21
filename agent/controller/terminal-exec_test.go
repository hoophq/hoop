package controller

import (
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
)

func TestDBExecResultMetadata(t *testing.T) {
	tests := []struct {
		name           string
		resultMetadata string
		clientVerb     string
		want           bool
	}{
		{"exec keeps the connection default", "on", pb.ClientVerbExec, true},
		{"exec honors RESULT_METADATA=off", "off", pb.ClientVerbExec, false},
		{"plain exec gets batch output", "on", pb.ClientVerbPlainExec, false},
		{"plain exec with RESULT_METADATA=off", "off", pb.ClientVerbPlainExec, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dbExecResultMetadata(&connEnv{resultMetadata: tt.resultMetadata}, tt.clientVerb)
			if got != tt.want {
				t.Errorf("dbExecResultMetadata(%q, %q) = %v, want %v", tt.resultMetadata, tt.clientVerb, got, tt.want)
			}
		})
	}
}
