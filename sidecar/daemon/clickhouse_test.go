package daemon

import (
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/inspect"
)

func TestClickHouseCodecFactoryIsConnectionScoped(t *testing.T) {
	f := clickhouseCodecFactory(inspect.ClickHouse, &ClickHouseCodecConfig{
		MaxFrameBytes: 1 << 20,
		MaxBlockBytes: 4 << 20,
	})
	if f == nil {
		t.Fatal("configured ClickHouse lane got no codec factory")
	}
	a, b := f(), f()
	if a == b {
		t.Fatal("two connections shared one stateful codec")
	}
	if a.Protocol() != inspect.ClickHouse || b.Protocol() != inspect.ClickHouse {
		t.Fatalf("factory protocols = %q, %q", a.Protocol(), b.Protocol())
	}
}

func TestClickHouseCodecConfigValidation(t *testing.T) {
	valid := ListenerConfig{
		Name: "warehouse", Protocol: "clickhouse", Listen: ":9000", Upstream: "db:9000",
		ClickHouse: &ClickHouseCodecConfig{MaxFrameBytes: 1 << 20, MaxBlockBytes: 8 << 20},
	}
	if problems := (&Config{}).validateLane(valid, valid.Name); len(problems) != 0 {
		t.Fatalf("valid config problems = %v", problems)
	}

	negative := valid
	negative.ClickHouse = &ClickHouseCodecConfig{MaxFrameBytes: -1, MaxBlockBytes: -1}
	problems := strings.Join((&Config{}).validateLane(negative, negative.Name), "\n")
	if !strings.Contains(problems, "max_frame_bytes") || !strings.Contains(problems, "max_block_bytes") {
		t.Fatalf("negative limits problems = %q", problems)
	}

	wrong := valid
	wrong.Protocol = "postgres"
	problems = strings.Join((&Config{}).validateLane(wrong, wrong.Name), "\n")
	if !strings.Contains(problems, "only valid on a clickhouse listener") {
		t.Fatalf("wrong-protocol problems = %q", problems)
	}
}

func TestLaneCodecFactoryKeepsRegistryDefaults(t *testing.T) {
	if f := laneCodecFactory(inspect.ClickHouse, nil, nil); f != nil {
		t.Fatal("unconfigured ClickHouse lane bypassed the registry default")
	}
	if f := laneCodecFactory(inspect.Postgres, nil, &ClickHouseCodecConfig{}); f != nil {
		t.Fatal("ClickHouse options affected a postgres lane")
	}
}
