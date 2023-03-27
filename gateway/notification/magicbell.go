package notification

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/common/log"
)

type (
	MagicBell struct {
		client    http.Client
		apiKey    string
		apiSecret string
	}
)

func NewMagicBell() *MagicBell {
	return &MagicBell{
		client:    http.Client{Timeout: 2 * time.Second},
		apiKey:    os.Getenv("MAGIC_BELL_API_KEY"),
		apiSecret: os.Getenv("MAGIC_BELL_API_SECRET"),
	}
}

func (m *MagicBell) Send(notification Notification) {
	if m.IsFullyConfigured() && len(notification.Recipients) > 0 {
		url := "https://api.magicbell.com/notifications"
		req, err := http.NewRequest(http.MethodPost, url, buildPayload(notification))
		if err != nil {
			log.Errorf("Failed building http request, err=%v", err)
			sentry.CaptureException(err)
			return
		}

		req.Header.Set("content-type", "application/json")
		req.Header.Set("X-MAGICBELL-API-KEY", m.apiKey)
		req.Header.Set("X-MAGICBELL-API-SECRET", m.apiSecret)

		resp, err := m.client.Do(req)
		if err != nil {
			log.Errorf("failed to send magic bell notification, err=%v", err)
			sentry.CaptureException(err)
			return
		}
		log.Infof("Sent notification to %d recipients", len(notification.Recipients))
		defer resp.Body.Close()
	}
}

func (m *MagicBell) IsFullyConfigured() bool {
	return m.apiKey != "" && m.apiSecret != ""
}

func buildPayload(notification Notification) io.Reader {
	p := map[string]any{
		"notification": map[string]any{
			"title":      notification.Title,
			"content":    notification.Message,
			"recipients": buildRecipients(notification.Recipients),
		},
	}

	payload, _ := json.Marshal(p)
	return bytes.NewBufferString(string(payload))
}

func buildRecipients(emails []string) []map[string]string {
	m := make([]map[string]string, 0)
	for _, e := range emails {
		m = append(m, map[string]string{"email": e})
	}
	return m
}
