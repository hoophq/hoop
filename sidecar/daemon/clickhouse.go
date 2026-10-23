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
	MaxFrameBytes int `json:"max_frame_bytes,omitempty" label:"Max frame bytes" help:"0 keeps the default of 16 MiB."`
	MaxBlockBytes int `json:"max_block_bytes,omitempty" label:"Max block bytes" help:"0 keeps the default of 64 MiB."`
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

// clickhouseCodecFactory builds a clickhouse lane's codec when it needs
// more than the registry default: configured limits, or a login the lane
// terminates in the codec itself (the native Hello carries user and
// password in one frame).
func clickhouseCodecFactory(proto inspect.Protocol, cfg *ClickHouseCodecConfig, auth *laneAuth, passthroughUsers []string) func() inspect.Codec {
	if proto != inspect.ClickHouse || (cfg == nil && auth == nil) {
		return nil
	}
	opts := codecclickhouse.Options{}
	if cfg != nil {
		opts.MaxFrameBytes, opts.MaxBlockBytes = cfg.MaxFrameBytes, cfg.MaxBlockBytes
	}
	if auth != nil {
		opts.Authenticate = auth.authenticator()
		opts.Passthrough = passthrough(passthroughUsers)
	}
	return func() inspect.Codec {
		return codecclickhouse.NewWithOptions(opts)
	}
}

// laneCodecFactory selects the one protocol-specific factory a listener may
// need. Nil deliberately preserves the registry path for default settings.
//
// An http lane is the exception and always gets its own: the registry's
// codec has no way to learn the lane's credential header, and a lane that
// lifts none still needs its capture settings. A plug-in lane always gets
// one too, because the registry never holds its protocol: plugins is the
// loaded set, and the caller proved the lane's module is in it
// (Config.codecPluginFor).
func laneCodecFactory(lc ListenerConfig, plugins map[inspect.Protocol]CodecPlugin) func() inspect.Codec {
	return authCodecFactory(lc, plugins, nil)
}

// authCodecFactory is laneCodecFactory for a lane that may terminate its
// login: auth reaches the http and clickhouse codecs, which swap the
// credential themselves; the relay's drivers take it from proxy.Config.
func authCodecFactory(lc ListenerConfig, plugins map[inspect.Protocol]CodecPlugin, auth *laneAuth) func() inspect.Codec {
	proto := inspect.Protocol(lc.Protocol)
	if plugin, ok := plugins[proto]; ok && isPluginLane(lc) {
		return codecPluginFactory(plugin, lc.Plugin)
	}
	if proto == inspect.HTTP {
		impersonate := lc.Propagate != nil && lc.Propagate.ImpersonateUser
		return newHTTPCodec(lc.HTTP, lc.credentialHeader(), lc.authenticatesCaller(), analyzerHolds(lc.Analyzer), lc.origin(), auth, impersonate)
	}
	return clickhouseCodecFactory(proto, lc.ClickHouse, auth, lc.PassthroughUsers)
}
