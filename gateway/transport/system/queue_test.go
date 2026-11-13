package transportsystem

import (
	"testing"
	"time"

	pbsystem "github.com/hoophq/hoop/common/proto/system"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Only an agent of the org that sent a request can answer it.
func TestSendIsOrgScoped(t *testing.T) {
	const sid = "00000000-0000-0000-0000-0000000011a7"
	ch, done := awaitResponse("org-a", sid)
	defer done()

	for _, orgID := range []string{"org-b", ""} {
		assert.Error(t, Send(orgID, pbsystem.BareExecResponseType, sid, []byte("forged")), "org %q", orgID)
	}
	assert.True(t, systemStore.Has(sid), "a refused answer must keep the request open")

	got := make(chan []byte, 1)
	go func() { got <- <-ch }()
	require.Eventually(t, func() bool {
		// Send drops the answer when the receiver is not waiting yet.
		systemStore.Set(sid, pendingResponse{orgID: "org-a", ch: ch})
		return Send("org-a", pbsystem.BareExecResponseType, sid, []byte("ok")) == nil
	}, time.Second, 5*time.Millisecond)
	assert.Equal(t, "ok", string(<-got))
	assert.False(t, systemStore.Has(sid))
}
