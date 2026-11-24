package slack

import (
	"testing"

	slackservice "github.com/hoophq/hoop/gateway/slack"
)

// A sidecar review has no Slack owner: the sidecar files it, not a user. Its
// approval posts nothing. A connection review still tells its owner.
func TestSendApprovedMessage(t *testing.T) {
	const orgID = "00000000-0000-0000-0000-0000000000e2"
	for _, tt := range []struct {
		name    string
		slackID string
		want    []string
	}{
		{"sidecar review without a Slack owner", "", nil},
		{"connection review with a Slack owner", "U-OWNER", []string{"U-OWNER"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeSlack{}
			slackservice.SetServiceInstance(orgID, newFakeSlackService(t, f))
			t.Cleanup(func() { slackservice.RemoveServiceInstance(orgID) })

			SendApprovedMessage(orgID, tt.slackID, "sid-1", "https://hoop.example")

			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.posts) != len(tt.want) || (len(tt.want) > 0 && f.posts[0] != tt.want[0]) {
				t.Fatalf("posted to %q, want %q", f.posts, tt.want)
			}
		})
	}
}
