package jobs

import (
	"fmt"
	"github.com/getsentry/sentry-go"
	"github.com/go-co-op/gocron"
	"github.com/runopsio/hoop/gateway/notification"
	"github.com/runopsio/hoop/gateway/user"
	"log"
	"time"
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
		CountSessions(org *user.Org) (int, error)
	}
)

func InitReportScheduler(s *Scheduler) {
	scheduler := gocron.NewScheduler(time.UTC)
	scheduler.Every(1).Minute().Do(func() {
		s.sendReports()
	})
	scheduler.StartAsync()
}

func (s *Scheduler) sendReports() {
	orgs, err := s.UserStorage.FindOrgs()
	if err != nil {
		log.Printf("scheduler job failed, err=%v", err)
		sentry.CaptureException(err)
		return
	}

	for _, o := range orgs {
		go s.sendReport(&o)
	}
}

func (s *Scheduler) sendReport(o *user.Org) {
	ctx := &user.Context{
		Org: o,
	}
	admins, err := s.UserStorage.FindByGroups(ctx, []string{user.GroupAdmin})
	if err != nil {
		log.Printf("send report failed, org=%s, err=%v", o.Name, err)
		sentry.CaptureException(err)
		return
	}
	sessionCount, err := s.SessionStorage.CountSessions(o)
	if err != nil {
		log.Printf("send report failed, org=%s, err=%v", o.Name, err)
		sentry.CaptureException(err)
		return
	}
	template := s.buildTemplate(o, sessionCount)

	fmt.Println("Sending admins weekly report")
	s.Notification.Send(notification.Notification{
		Title:      "Your weekly report at Hoop",
		Message:    template,
		Recipients: listEmails(admins),
	})
}

func (s *Scheduler) buildTemplate(o *user.Org, sessionCount int) string {
	return fmt.Sprintf("Hi %s administrator, You had %d sessions this week. Congratulations.", o.Name, sessionCount)
}

func listEmails(reviewers []user.User) []string {
	emails := make([]string, 0)
	for _, r := range reviewers {
		emails = append(emails, r.Email)
	}
	return emails
}
