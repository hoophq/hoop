package dsnkeys

import (
	"fmt"
	"testing"

	"github.com/hoophq/hoop/common/proto"
)

func TestDsnKeysMatch(t *testing.T) {
	for _, tt := range []struct {
		msg  string
		err  error
		url  string
		name string
		sk   string
		mode string
	}{
		{
			msg:  "it must match with standard agent mode",
			url:  "http://127.0.0.1:8009",
			name: "prod",
			sk:   "sk-secure",
			mode: proto.AgentModeStandardType,
		},
		{
			msg:  "it must match with embedded agent mode",
			url:  "grpc://127.0.0.1:8010",
			name: "prod",
			sk:   "sk-secure",
			mode: proto.AgentModeEmbeddedType,
		},
		{
			msg:  "it must match with multi-connection agent mode",
			url:  "grpc://127.0.0.1:8010",
			name: "prod",
			sk:   "sk-secure",
			mode: proto.AgentModeMultiConnectionType,
		},
		{
			msg:  "it must fail with parse url error",
			url:  "",
			name: "",
			sk:   "",
			mode: "",
			err:  fmt.Errorf(`parse "://:@:?mode=": missing protocol scheme`),
		},
		{
			msg:  "it must fail when secret is not found in the dsn",
			url:  "http://127.0.0.1:8009",
			name: "prod",
			sk:   "",
			mode: proto.AgentModeStandardType,
			err:  ErrSecretKeyNotFound,
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			dsnKey, err := NewString(tt.url, tt.name, tt.sk, tt.mode)
			if fmt.Sprintf("%v", err) != fmt.Sprintf("%v", tt.err) {
				t.Fatalf("expect errors to be equal, want=%v, got=%v", tt.err, err)
			}
			dsn, _ := Parse(dsnKey)
			if dsn == nil && tt.err != nil {
				return
			}
			gotSkHash, _ := hash256Key(tt.sk)
			if fmt.Sprintf("%s://%s", dsn.Scheme, dsn.Address) == tt.url && dsn.Name == tt.name &&
				dsn.SecretKeyHash == gotSkHash && dsn.AgentMode == tt.mode {
				return
			}
			t.Errorf("expect dsn to match, want=%#v, got=%#v", tt, *dsn)
		})
	}
}
