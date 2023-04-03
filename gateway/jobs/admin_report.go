package jobs

import (
	"fmt"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/go-co-op/gocron"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/notification"
	"github.com/runopsio/hoop/gateway/session"
	"github.com/runopsio/hoop/gateway/user"
)

type (
	Scheduler struct {
		UserStorage    userStorage
		SessionStorage sessionStorage
		Notification   notification.Service
	}

	userStorage interface {
		FindOrgs() ([]user.Org, error)
		FindByGroups(context *user.Context, groups []string) ([]user.User, error)
	}

	sessionStorage interface {
		FindAll(ctx *user.Context, opts ...*session.SessionOption) (*session.SessionList, error)
	}
)

func InitReportScheduler(s *Scheduler) {
	scheduler := gocron.NewScheduler(time.UTC)
	scheduler.Every(1).Day().At("20:00").Do(func() {
		s.sendReports()
	})
	scheduler.StartAsync()
}

func (s *Scheduler) sendReports() {
	fmt.Println("Starting sendReports job")
	orgs, err := s.UserStorage.FindOrgs()
	if err != nil {
		log.Printf("scheduler job failed, err=%v", err)
		sentry.CaptureException(err)
		return
	}

	for _, o := range orgs {
		if o.Name == "runops" { // TODO remove later, "feature flag"
			go s.sendReport(o)
		}
	}
}

func (s *Scheduler) sendReport(o user.Org) {
	log.Printf("Sending report to %s\n", o.Name)
	ctx := &user.Context{
		Org: &o,
	}
	admins, err := s.UserStorage.FindByGroups(ctx, []string{user.GroupAdmin})
	if err != nil {
		log.Printf("send report failed, org=%s, err=%v", o.Name, err)
		sentry.CaptureException(err)
		return
	}

	var options []*session.SessionOption
	startDate := time.Now().UTC().AddDate(0, 0, -7)
	endDate := time.Now().UTC()
	options = append(options,
		session.WithOption(session.OptionStartDate, startDate),
		session.WithOption(session.OptionEndDate, endDate))

	sessionList, err := s.SessionStorage.FindAll(ctx, options...)
	if err != nil {
		log.Printf("send report failed, org=%s, err=%v", o.Name, err)
		sentry.CaptureException(err)
		return
	}
	dlpCount := int64(0)
	for _, s := range sessionList.Items {
		dlpCount += s.DlpCount
	}
	template := s.buildTemplate(&o, sessionList.Total, dlpCount)

	log.Info("Sending admins weekly report")
	s.Notification.Send(notification.Notification{
		Title:      "Your weekly report at Hoop",
		Message:    template,
		Recipients: listEmails(admins),
	})
}

func (s *Scheduler) buildTemplate(o *user.Org, sessionCount int, dlpCount int64) string {
	return fmt.Sprintf("Hi %s administrator, You had %d session(s) executed and %d DLP fields redacted in the past 7 days.",
		o.Name, sessionCount, dlpCount)
}

func listEmails(reviewers []user.User) []string {
	emails := make([]string, 0)
	for _, r := range reviewers {
		emails = append(emails, r.Email)
	}
	return emails
}
