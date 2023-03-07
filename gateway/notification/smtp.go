package notification

import (
	"fmt"
	"net/smtp"
	"os"
)

type (
	SmtpSender struct {
		host     string
		port     string
		user     string
		password string
		from     string
	}
)

func NewSmtpSender() *SmtpSender {
	return &SmtpSender{
		host:     os.Getenv("SMTP_HOST"),
		port:     os.Getenv("SMTP_PORT"),
		user:     os.Getenv("SMTP_USER"),
		password: os.Getenv("SMTP_PASS"),
		from:     os.Getenv("SMTP_FROM"),
	}
}

func (s *SmtpSender) Send(notification Notification) {
	if !s.isFullyConfigured() {
		return
	}
	if s.from == "" {
		s.from = "no-reply@hoop.dev"
	}

	body := []byte(notification.Message)
	auth := smtp.PlainAuth(s.user, s.from, s.password, s.host)
	err := smtp.SendMail(s.address(), auth, s.from, notification.Recipients, body)
	if err != nil {
		fmt.Println(err)
	}
}

func (s *SmtpSender) isFullyConfigured() bool {
	return s.host != "" &&
		s.port != "" &&
		s.user != "" &&
		s.password != ""
}

func (s *SmtpSender) address() string {
	return s.host + ":" + s.port
}
