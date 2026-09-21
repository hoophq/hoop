// Package clickhouse registers the ClickHouse native-protocol codec with the
// inspect registry.
//
// The decoder lives in github.com/hoophq/libhoop/v2/codec/clickhouse. This
// package is the dependency seam: it injects sidecar's SQL classifier and its
// ClickHouse lexer without making libhoop import the main project.
package clickhouse

import (
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
	codecclickhouse "github.com/hoophq/libhoop/v2/codec/clickhouse"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

// New builds one connection-scoped codec with conservative memory defaults.
// A listener that needs larger legitimate blocks uses NewWithLimits; neither
// path permits an unbounded decompression size.
func New() inspect.Codec {
	return NewWithLimits(0, 0)
}

// NewWithLimits builds one connection-scoped codec with explicit compressed
// frame and decompressed block caps. Zero selects libhoop's bounded default.
func NewWithLimits(maxFrameBytes, maxBlockBytes int) inspect.Codec {
	return codecclickhouse.New(codecclickhouse.Options{
		Analyze:       inspect.AnalyzeSQL,
		Split:         split,
		MaxFrameBytes: maxFrameBytes,
		MaxBlockBytes: maxBlockBytes,
	})
}

func split(sql string, _ codectypes.Protocol) []string {
	return lexer.Split(sql, lexer.ClickHouse)
}

func init() {
	inspect.Register(func() inspect.Codec { return New() })
}
