package jobs

import (
	"time"

	"github.com/go-co-op/gocron"
	"github.com/hoophq/hoop/common/log"
	jobsessions "github.com/hoophq/hoop/gateway/jobs/sessions"
	plugintypes "github.com/hoophq/hoop/gateway/transport/plugins/types"
)

func Run() {
	log.Infof("starting job scheduler (every 30m)")
	scheduler := gocron.NewScheduler(time.UTC)

	_, err := scheduler.
		SingletonMode().
		Cron("*/30 * * * *").
		DoWithJobDetails(jobsessions.ProcessWalSessions, plugintypes.AuditPath)
	if err != nil {
		log.Fatalf("failed scheduling wal sessions job, reason=%v", err)
	}
	scheduler.StartBlocking()
}
