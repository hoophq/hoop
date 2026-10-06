package slack

import (
	"testing"

	slackservice "github.com/hoophq/hoop/gateway/slack"
)

// A socket that fails marks its org down so the next sync restarts it. A
// socket that ends because its service was replaced is the restart itself and
// must not mark the replacement down.
func TestMarkSocketDownOnlyForTheCurrentService(t *testing.T) {
	const org = "org-socket-down"
	old, current := &slackservice.SlackService{}, &slackservice.SlackService{}
	slackservice.SetServiceInstance(org, current)
	t.Cleanup(func() { slackservice.RemoveServiceInstance(org) })

	p := &slackPlugin{running: map[string]runningService{org: {socket: true}}}
	p.markSocketDown(org, old)
	if !p.running[org].socket {
		t.Fatal("a replaced service marked its replacement's socket down")
	}
	p.markSocketDown(org, current)
	if p.running[org].socket {
		t.Fatal("the current service's failed socket was not marked down")
	}
}
