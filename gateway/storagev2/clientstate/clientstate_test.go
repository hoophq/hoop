package clientstate

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/runopsio/hoop/gateway/pgrest"
	"github.com/runopsio/hoop/gateway/storagev2"
	"github.com/runopsio/hoop/gateway/storagev2/types"
	"olympos.io/encoding/edn"
)

type clientFunc func(req *http.Request) (*http.Response, error)

func (f clientFunc) Do(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newFakeStorageContext(fn clientFunc) *storagev2.Context {
	return storagev2.NewContext("noop-id", "noop-org", storagev2.NewStorage(fn))
}

func newFakeEntity(xtID, sessionID string) *types.Client {
	return &types.Client{
		ID:                    xtID,
		OrgID:                 "test-org",
		Status:                types.ClientStatusConnected,
		RequestConnectionName: "pg",
		RequestPort:           "5433",
		RequestAccessDuration: time.Second * 10,
		ClientMetadata:        map[string]string{"session": sessionID}}
}

func TestGetEntity(t *testing.T) {
	pgrest.Rollout = false
	for _, tt := range []struct {
		msg    string
		ctx    *storagev2.Context
		entity *types.Client
		err    error
	}{
		{
			msg: "entity must match with response",
			ctx: newFakeStorageContext(clientFunc(func(req *http.Request) (*http.Response, error) {
				data, _ := edn.Marshal(newFakeEntity("123", "a-random-session-id"))
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(data)),
				}, nil
			})),
			entity: newFakeEntity("123", "a-random-session-id"),
		},
		{
			msg: "it must return empty entity",
			ctx: newFakeStorageContext(clientFunc(func(req *http.Request) (*http.Response, error) {
				data := []byte(`{:error "entity not found"}`)
				return &http.Response{
					StatusCode: http.StatusNotFound,
					Body:       io.NopCloser(bytes.NewReader(data)),
				}, nil
			})),
			entity: nil,
		},
		{
			msg: "it must return error",
			ctx: newFakeStorageContext(clientFunc(func(req *http.Request) (*http.Response, error) {
				data := []byte(`{:error "bad gateway error"}`)
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Body:       io.NopCloser(bytes.NewReader(data)),
				}, nil
			})),
			entity: nil,
			err: fmt.Errorf(`failed fetching entity, status=%v, data={:error "bad gateway error"}`,
				http.StatusBadGateway),
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			resp, err := GetEntity(tt.ctx, "")
			if tt.err != nil && err.Error() != tt.err.Error() {
				t.Fatalf("want error=%v, got=%v", tt.err, err)
			}
			if tt.err == nil && err != nil {
				t.Fatalf("it did not expect error, got=%v", err)
			}
			if tt.entity == nil && resp != nil {
				t.Fatalf("it expected entity to be nil, got=%v", *resp)
			}
			if !reflect.DeepEqual(tt.entity, resp) {
				t.Errorf("expect entities to be equal, want=%v, got=%v", *tt.entity, *resp)
			}
		})
	}
}
