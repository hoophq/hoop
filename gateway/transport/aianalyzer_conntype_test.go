package transport

import (
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
)

// Which connection types carry an AI analyzer config to the agent.
//
// This is the gate that decides whether the feature exists for a connection at
// all. Getting it wrong is silent in both directions: withhold the config and
// an admin's analyzer rule does nothing with no error anywhere; ship it to a
// type the agent does not analyze and the config is dead weight on the wire.
func TestAgentEnforcesAIAnalysis(t *testing.T) {
	analyzed := []pb.ConnectionType{
		pb.ConnectionTypeHttpProxy,
		pb.ConnectionTypeKubernetes,
		// mcpproxy analyzes tool calls in the MCP inspection pipeline.
		pb.ConnectionTypeMcpProxy,
	}
	for _, ct := range analyzed {
		if !agentEnforcesAIAnalysis(ct) {
			t.Errorf("%s gets no analyzer config; a configured rule would silently do nothing", ct)
		}
	}

	// Everything else is either analyzed gateway-side on the exec path (the
	// whole script, once, before the session opens) or not analyzed at all.
	notAnalyzed := []pb.ConnectionType{
		pb.ConnectionTypePostgres,
		pb.ConnectionTypeMySQL,
		pb.ConnectionTypeMongoDB,
		pb.ConnectionTypeSSH,
		pb.ConnectionTypeTCP,
		pb.ConnectionTypeCommandLine,
		// The legacy "mcp" subtype runs through the byte-relay httpproxy path
		// with no inspection pipeline, so it has nowhere to put a verdict.
		pb.ConnectionTypeMcp,
	}
	for _, ct := range notAnalyzed {
		if agentEnforcesAIAnalysis(ct) {
			t.Errorf("%s ships an analyzer config the agent never consults", ct)
		}
	}
}
