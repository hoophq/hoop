package clickhouse_test

import (
	"testing"

	_ "github.com/hoophq/hoop/sidecar/codec/clickhouse"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
)

func TestRegistryBuildsFullClickHouseCodec(t *testing.T) {
	insp, err := inspect.New(inspect.ClickHouse)
	if err != nil {
		t.Fatalf("inspect.New: %v", err)
	}
	codec := insp.Codec()
	if codec.Protocol() != inspect.ClickHouse {
		t.Fatalf("protocol = %q", codec.Protocol())
	}
	if _, ok := codec.(gate.Duplex); !ok {
		t.Fatal("ClickHouse codec did not declare shared bidirectional state")
	}
	if _, ok := codec.(gate.StreamFilter); !ok {
		t.Fatal("ClickHouse codec did not expose its hello revision clamp")
	}
	if _, ok := codec.(gate.Reframer); !ok {
		t.Fatal("ClickHouse codec did not expose response masking")
	}
	if !gate.MaskSupported(inspect.ClickHouse) {
		t.Fatal("ClickHouse registration is not reported as mask-capable")
	}
}
