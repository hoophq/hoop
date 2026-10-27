package liveeventsapi

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hoophq/hoop/gateway/liveevents"
	"github.com/hoophq/hoop/gateway/storagev2"
)

func streamAs(t *testing.T, ctx *storagev2.Context) (frames <-chan string, ended <-chan struct{}) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stream", func(c *gin.Context) { c.Set(storagev2.ContextKey, ctx) }, Stream)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	reqCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, srv.URL+"/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out := make(chan string, 16)
	end := make(chan struct{})
	go func() {
		defer close(end)
		defer res.Body.Close()
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				out <- data
			}
		}
	}()
	return out, end
}

func drain(ch <-chan string) []string {
	var got []string
	for {
		select {
		case f := <-ch:
			got = append(got, f)
		case <-time.After(300 * time.Millisecond):
			return got
		}
	}
}

func user(org, id string, groups ...string) *storagev2.Context {
	return storagev2.NewContext(id, org).WithUserInfo(id, id+"@hoop.dev", "active", "", groups)
}

func TestEachEventTypeReachesItsOwnAudience(t *testing.T) {
	const org = "org-stream-audience"
	admin, _ := streamAs(t, user(org, "admin-1", "admin"))
	reviewer, _ := streamAs(t, user(org, "reviewer-1", "dba"))
	owner, _ := streamAs(t, user(org, "owner-1", "dba"))
	auditor, _ := streamAs(t, user(org, "auditor-1", "auditor"))

	liveevents.Publish(org, liveevents.Event{Type: liveevents.SidecarCheckedIn, ID: "sc-1"})
	liveevents.Publish(org, liveevents.Event{
		Type: liveevents.ReviewCreated, ID: "rev-1",
		Audience: &liveevents.Audience{Groups: []string{"dba"}, OwnerID: "owner-1"},
	})

	for name, tc := range map[string]struct {
		frames <-chan string
		want   []string
	}{
		"admin":    {admin, []string{`{"type":"sidecar.checked_in","id":"sc-1"}`}},
		"reviewer": {reviewer, []string{`{"type":"review.created","id":"rev-1"}`}},
		"owner":    {owner, nil},
		"auditor":  {auditor, nil},
	} {
		got := drain(tc.frames)
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%s got %q, want %q", name, got, tc.want)
		}
	}
}

func TestAStreamEndsAtItsMaximumAge(t *testing.T) {
	old := maxStreamAge
	maxStreamAge = 50 * time.Millisecond
	t.Cleanup(func() { maxStreamAge = old })

	_, ended := streamAs(t, user("org-stream-age", "admin-1", "admin"))
	select {
	case <-ended:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream outlived its maximum age")
	}
}
