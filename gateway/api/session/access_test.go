package sessionapi

import (
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/storagev2"
)

// A denial row records who denied, not a reviewer group. A member of the
// requester's group must not open the session the review read hides from them.
func TestCanAccessSessionSkipsDenialRows(t *testing.T) {
	sess := &models.Session{
		UserID: "requester",
		Review: &models.SessionReview{ReviewGroups: []models.ReviewGroups{
			{GroupName: "dba"},
			{GroupName: "eng", AddedOnDenial: true},
		}},
	}
	for _, tt := range []struct {
		user, group string
		want        bool
	}{
		{"requester", "eng", true},
		{"dba-member", "dba", true},
		{"teammate", "eng", false},
	} {
		ctx := storagev2.NewContext(tt.user, "org").WithUserInfo(tt.user, tt.user+"@hoop.dev", "active", "", []string{tt.group})
		if got := canAccessSession(ctx, sess); got != tt.want {
			t.Errorf("%s in %s: got %v, want %v", tt.user, tt.group, got, tt.want)
		}
	}
}
