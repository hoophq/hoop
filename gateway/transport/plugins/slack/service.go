package slack

import (
	"bytes"
	"encoding/json"
	"github.com/getsentry/sentry-go"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/review"
	"github.com/runopsio/hoop/gateway/user"
	"net/http"
	"time"
)

type (
	Message struct {
		Kind string `json:"kind"`
		Data Data   `json:"data"`
	}

	Data struct {
		Name           string   `json:"name"`
		Email          string   `json:"email"`
		Groups         []string `json:"groups"`
		Type           string   `json:"type"`
		ConnectionName string   `json:"connection_name"`
		Script         string   `json:"script"`
		ReviewID       string   `json:"review_id"`
		ReviewURI      string   `json:"review_uri"`
	}

	Slack struct {
		baseURL string
		client  http.Client
	}
)

const (
	reviewKind = "review"
)

var slack = Slack{
	baseURL: "http://127.0.0.1:8012/api/messages",
	client:  http.Client{Timeout: 2 * time.Second},
}

func SendReviewMsg(u *user.User, r review.Review, reviewURI, connType string) {
	groups := parseGroups(r.ReviewGroups)

	payload, _ := json.Marshal(Message{
		Kind: reviewKind,
		Data: Data{
			Name:           u.Name,
			Email:          u.Email,
			Groups:         groups,
			Type:           connType,
			ConnectionName: r.Connection.Name,
			Script:         r.Input,
			ReviewID:       r.Id,
			ReviewURI:      reviewURI,
		},
	})

	req, err := http.NewRequest(http.MethodPost, slack.baseURL, bytes.NewBufferString(string(payload)))
	if err != nil {
		log.Errorf("Failed building http request, err=%v", err)
		sentry.CaptureException(err)
		return
	}

	req.Header.Set("content-type", "application/json")

	resp, err := slack.client.Do(req)
	if err != nil {
		log.Errorf("failed to send slack message, err=%v", err)
		sentry.CaptureException(err)
		return
	}
	defer resp.Body.Close()
}

func parseGroups(reviewGroups []review.Group) []string {
	groups := make([]string, 0)
	for _, g := range reviewGroups {
		groups = append(groups, g.Group)
	}
	return groups
}
