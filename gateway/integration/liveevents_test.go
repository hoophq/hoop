//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/liveevents"
	"github.com/hoophq/hoop/gateway/models"
)

func TestLiveEventsReachEveryReplica(t *testing.T) {
	if testGateway.Postgres == nil {
		t.Skip("PGlite serves one session: live events stay in-process")
	}
	dsn := testGateway.Postgres.URI()
	sqlDB, err := models.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	listening := func() int {
		var n int
		err := models.DB.Raw(`SELECT count(*) FROM pg_stat_activity WHERE query = 'LISTEN hoop_liveevents'`).Scan(&n).Error
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting: %s", what)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, b := liveevents.NewHub(), liveevents.NewHub()
	go a.Run(ctx, dsn, sqlDB)
	go b.Run(ctx, dsn, sqlDB)
	waitFor("one LISTEN connection per replica", func() bool { return listening() == 2 })

	onA, stopA := a.Subscribe(testGateway.OrgID)
	defer stopA()
	onB, stopB := b.Subscribe(testGateway.OrgID)
	defer stopB()

	ev := liveevents.Event{Type: liveevents.SidecarCheckedIn, ID: "sc-across"}
	b.Publish(testGateway.OrgID, ev)
	for name, ch := range map[string]<-chan liveevents.Event{"A": onA, "B": onB} {
		select {
		case got := <-ch:
			if got != ev {
				t.Fatalf("replica %s got %+v", name, got)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("replica %s got no event", name)
		}
	}
	time.Sleep(500 * time.Millisecond)
	if len(onA) > 0 || len(onB) > 0 {
		t.Fatalf("an event arrived twice: %d more on A, %d more on B", len(onA), len(onB))
	}

	cancel()
	waitFor("the LISTEN connections close", func() bool { return listening() == 0 })
}
