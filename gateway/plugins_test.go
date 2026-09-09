package gateway

import (
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

// names is what a boot path publishes as RegisteredPlugins, in order.
func names(plugins []plugintypes.Plugin) []string {
	got := make([]string, len(plugins))
	for i, p := range plugins {
		got[i] = p.Name()
	}
	return got
}

// The chain is ordered, and the order is load bearing: review must decide
// before audit records, and both must run before the packet reaches Slack.
// CLAUDE.md says do not reorder casually; this is the test that notices.
func TestGatewayPluginsOrder(t *testing.T) {
	want := []string{
		plugintypes.PluginReviewName,
		plugintypes.PluginAuditName,
		plugintypes.PluginDLPName,
		plugintypes.PluginAccessControlName,
		plugintypes.PluginWebhookName,
		plugintypes.PluginSlackName,
	}
	got := names(gatewayPlugins("http://localhost:8009", nil))
	if len(got) != len(want) {
		t.Fatalf("gateway chain = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("gateway plugin %d = %q, want %q (full chain %v)", i, got[i], want[i], got)
		}
	}
}

// The control plane carries no packets, so a plugin here earns its place by
// what it starts. Slack owns the socket an approver clicks a button on. The
// other five would be dead weight, and one of them, audit, writes to disk.
func TestControlPlanePluginsIsSlackOnly(t *testing.T) {
	got := names(controlPlanePlugins(nil))
	want := []string{plugintypes.PluginSlackName}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("control plane chain = %v, want %v", got, want)
	}
}

// A control plane must never run a plugin the gateway does not, or the two
// modes disagree about what a plugin name means.
func TestControlPlanePluginsAreASubsetOfTheGatewayChain(t *testing.T) {
	inGateway := map[string]bool{}
	for _, name := range names(gatewayPlugins("http://localhost:8009", nil)) {
		inGateway[name] = true
	}
	for _, name := range names(controlPlanePlugins(nil)) {
		if !inGateway[name] {
			t.Errorf("control plane starts %q, which the gateway chain does not contain", name)
		}
	}
}

// stubPlugin records that it was started. The real chain is not usable here:
// Slack's OnStartup reads organizations from the database, and startPlugins
// treats a failure as fatal.
type stubPlugin struct {
	name    string
	started *[]string
}

func (p stubPlugin) Name() string { return p.name }
func (p stubPlugin) OnStartup(plugintypes.Context) error {
	*p.started = append(*p.started, p.name)
	return nil
}
func (p stubPlugin) OnUpdate(_, _ plugintypes.PluginResource) error { return nil }
func (p stubPlugin) OnConnect(plugintypes.Context) error            { return nil }
func (p stubPlugin) OnDisconnect(plugintypes.Context, error) error  { return nil }
func (p stubPlugin) OnReceive(plugintypes.Context, *pb.Packet) (*plugintypes.ConnectResponse, error) {
	return nil, nil
}

// startPlugins publishes the set it started, in order. A handler that reaches
// a plugin through RegisteredPlugins, such as PUT /plugins/slack/config, sees
// exactly what booted and nothing else.
func TestStartPluginsPublishesWhatItStarted(t *testing.T) {
	t.Cleanup(func() { plugintypes.RegisteredPlugins = nil })

	var started []string
	startPlugins([]plugintypes.Plugin{
		stubPlugin{name: "first", started: &started},
		stubPlugin{name: "second", started: &started},
	})

	if len(started) != 2 || started[0] != "first" || started[1] != "second" {
		t.Errorf("OnStartup ran for %v, want [first second]", started)
	}
	if got := names(plugintypes.RegisteredPlugins); len(got) != 2 || got[0] != "first" || got[1] != "second" {
		t.Errorf("RegisteredPlugins = %v, want [first second]", got)
	}
}
