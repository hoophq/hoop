package featureflagrefresh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/common/featureflag"
	pb "github.com/hoophq/hoop/common/proto"
	"github.com/hoophq/hoop/gateway/api/featureflags"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/pglite/pglitetest"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/gateway/storagev2"
	"github.com/hoophq/hoop/gateway/storagev2/types"
	"github.com/hoophq/hoop/gateway/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { os.Exit(pglitetest.Main(m)) }

const (
	refreshOrgID = "00000000-0000-0000-0000-0000000000f2"
	otherOrgID   = "00000000-0000-0000-0000-0000000000f3"
	flag         = featureflag.FlagAnalyzeBeforeRunForms
)

// startReplicas starts one database with two orgs, and returns replica A's
// cache (featureflag.Default, which the API handler writes) and replica B's,
// both loaded from it.
func startReplicas(t *testing.T) (replicaA, replicaB *featureflag.Cache) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping embedded database test in -short mode")
	}
	inst := pglitetest.StartMigrated(t)
	require.NoError(t, models.InitDatabaseConnection(inst.DSN(), 1))
	for _, org := range []string{refreshOrgID, otherOrgID} {
		require.NoError(t, models.DB.Exec(
			`INSERT INTO private.orgs (id, name) VALUES (?, ?)`, org, "featureflag-refresh-"+org).Error)
	}
	replicaA, replicaB = featureflag.Default(), featureflag.NewCache()
	t.Cleanup(func() {
		replicaA.SetAll(refreshOrgID, map[string]bool{})
		replicaA.SetAll(otherOrgID, map[string]bool{})
	})
	for _, c := range []*featureflag.Cache{replicaA, replicaB} {
		_, err := services.RefreshFeatureFlagCache(context.Background(), models.DB, c)
		require.NoError(t, err)
	}
	return replicaA, replicaB
}

// putFlag runs PUT /api/feature-flags/:name as an admin of org, on the
// replica whose cache is featureflag.Default.
func putFlag(t *testing.T, org string, enabled bool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Set(storagev2.ContextKey, storagev2.NewContext("admin@hoop.dev", org).
		WithUserInfo("Admin", "admin@hoop.dev", "active", "", []string{types.GroupAdmin}))
	c.Request = httptest.NewRequest(http.MethodPut, "/api/feature-flags/"+flag,
		strings.NewReader(fmt.Sprintf(`{"enabled": %v}`, enabled)))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "name", Value: flag}}
	featureflags.Update(c)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// Two replicas on one database: A answers the PUT, B learns of it only from
// the refresh loop. A flag turned off on A must stop on B too.
func TestAFlagChangeOnOneReplicaReachesTheOther(t *testing.T) {
	replicaA, replicaB := startReplicas(t)

	putFlag(t, refreshOrgID, true)
	require.True(t, replicaA.IsEnabled(refreshOrgID, flag))
	require.False(t, replicaB.IsEnabled(refreshOrgID, flag), "B has not refreshed yet")

	var mu sync.Mutex
	var notified []string
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(ctx, models.DB, replicaB, func() time.Duration { return 20 * time.Millisecond }, func(orgID string) {
			mu.Lock()
			defer mu.Unlock()
			notified = append(notified, orgID)
		})
	}()
	t.Cleanup(func() { cancel(); <-done })

	assert.Eventually(t, func() bool { return replicaB.IsEnabled(refreshOrgID, flag) },
		5*time.Second, 10*time.Millisecond, "B reads the flag A turned on")

	putFlag(t, refreshOrgID, false)
	assert.False(t, replicaA.IsEnabled(refreshOrgID, flag))
	assert.Eventually(t, func() bool { return !replicaB.IsEnabled(refreshOrgID, flag) },
		5*time.Second, 10*time.Millisecond, "B stops the flag A turned off")

	// B tells its agents once per change, and not on a read that changed nothing.
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{refreshOrgID, refreshOrgID}, notified)
}

func TestTheRefreshIntervalStaysWithinTheBound(t *testing.T) {
	for range 10_000 {
		d := nextInterval()
		require.GreaterOrEqual(t, d, baseInterval)
		require.Less(t, d, MaxInterval)
	}
	assert.LessOrEqual(t, MaxInterval, 15*time.Second)
}

// fakeAgent is an agent stream on replica B. With block set, Send waits until
// block is closed, as a gRPC send to an agent that does not read.
type fakeAgent struct {
	org   string
	block chan struct{}

	mu   sync.Mutex
	last map[string]bool
}

func (a *fakeAgent) Send(p *pb.Packet) error {
	if a.block != nil {
		<-a.block
	}
	var flags map[string]bool
	if err := json.Unmarshal(p.Spec[pb.SpecFeatureFlagsKey], &flags); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.last = flags
	return nil
}
func (a *fakeAgent) GetOrgID() string  { return a.org }
func (a *fakeAgent) AgentName() string { return "agent-" + a.org }

func (a *fakeAgent) flagOn() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.last[flag]
}

// An agent that does not read stops neither the database reads nor the
// delivery to the other agents, and the loop still stops on cancel.
func TestABlockedAgentDoesNotStopTheRefresh(t *testing.T) {
	_, replicaB := startReplicas(t)

	stuck := &fakeAgent{org: refreshOrgID, block: make(chan struct{})}
	defer close(stuck.block)
	healthy := &fakeAgent{org: refreshOrgID}
	other := &fakeAgent{org: otherOrgID}
	agents := map[string][]transport.FeatureFlagStream{refreshOrgID: {stuck, healthy}, otherOrgID: {other}}
	delivery := transport.NewFeatureFlagDelivery(
		func(org string) []transport.FeatureFlagStream { return agents[org] }, replicaB.SnapshotForOrg)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		run(ctx, models.DB, replicaB, func() time.Duration { return 20 * time.Millisecond }, delivery.Notify)
	}()

	putFlag(t, refreshOrgID, true)
	assert.Eventually(t, healthy.flagOn, 5*time.Second, 10*time.Millisecond, "the healthy agent of the same org")

	putFlag(t, otherOrgID, true)
	assert.Eventually(t, func() bool { return replicaB.IsEnabled(otherOrgID, flag) },
		5*time.Second, 10*time.Millisecond, "B keeps reading the database")
	assert.Eventually(t, other.flagOn, 5*time.Second, 10*time.Millisecond, "the agent of another org")

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the loop did not stop on cancel")
	}
}

// A failed read keeps the flags B has and tells no agent; the next good read
// applies the change.
func TestAFailedReadKeepsTheLastFlags(t *testing.T) {
	_, replicaB := startReplicas(t)
	putFlag(t, refreshOrgID, true)

	var notified []string
	notify := func(org string) { notified = append(notified, org) }

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	refresh(canceled, models.DB, replicaB, notify)
	assert.False(t, replicaB.IsEnabled(refreshOrgID, flag), "a canceled read changes nothing")

	require.NoError(t, models.DB.Exec(`ALTER TABLE private.org_feature_flags RENAME TO org_feature_flags_gone`).Error)
	refresh(context.Background(), models.DB, replicaB, notify)
	assert.False(t, replicaB.IsEnabled(refreshOrgID, flag), "a failed read changes nothing")
	assert.Empty(t, notified)

	require.NoError(t, models.DB.Exec(`ALTER TABLE private.org_feature_flags_gone RENAME TO org_feature_flags`).Error)
	refresh(context.Background(), models.DB, replicaB, notify)
	assert.True(t, replicaB.IsEnabled(refreshOrgID, flag), "the next good read applies the change")
	assert.Equal(t, []string{refreshOrgID}, notified)
}
