package daemon

import (
	"fmt"

	codecclickhouse "github.com/hoophq/hoop/sidecar/codec/clickhouse"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// ClickHouseCodecConfig bounds the codec's declared compressed frame and
// decompressed block sizes. The gate derives its bounded wire reassembly limit
// from these values. Both are bytes. Zero keeps the codec defaults (16 MiB per
// frame, 64 MiB per decompressed block).
type ClickHouseCodecConfig struct {
	MaxFrameBytes int `json:"max_frame_bytes,omitempty"`
	MaxBlockBytes int `json:"max_block_bytes,omitempty"`
}

func (c *ClickHouseCodecConfig) validate(lane string) []string {
	if c == nil {
		return nil
	}
	var problems []string
	if c.MaxFrameBytes < 0 {
		problems = append(problems, fmt.Sprintf(
			"listener %q: clickhouse.max_frame_bytes is negative", lane))
	}
	if c.MaxBlockBytes < 0 {
		problems = append(problems, fmt.Sprintf(
			"listener %q: clickhouse.max_block_bytes is negative", lane))
	}
	return problems
}

func clickhouseCodecFactory(proto inspect.Protocol, cfg *ClickHouseCodecConfig) func() inspect.Codec {
	if cfg == nil || proto != inspect.ClickHouse {
		return nil
	}
	return func() inspect.Codec {
		return codecclickhouse.NewWithLimits(cfg.MaxFrameBytes, cfg.MaxBlockBytes)
	}
}

// laneCodecFactory selects the one protocol-specific factory a listener may
// need. Nil deliberately preserves the registry path for default settings.
func laneCodecFactory(proto inspect.Protocol, http *HTTPCodecConfig, clickhouse *ClickHouseCodecConfig) func() inspect.Codec {
	if f := httpCodecFactory(proto, http); f != nil {
		return f
	}
	return clickhouseCodecFactory(proto, clickhouse)
}
