package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// fakeReviews answers one review, or err for every id.
type fakeReviews struct {
	rev ReviewStatus
	err error
}

func (f fakeReviews) ReviewStatus(_ context.Context, id string) (ReviewStatus, error) {
	if f.err != nil {
		return ReviewStatus{}, f.err
	}
	if id != f.rev.ID {
		return ReviewStatus{}, ErrReviewNotFound
	}
	return f.rev, nil
}

func (f fakeReviews) ListReviews(context.Context, string, int) ([]ReviewStatus, error) {
	return []ReviewStatus{f.rev}, f.err
}

const laneReviewID = "0b0e6f4e-8f3e-4a8e-9a39-1b1b7c1f2a10"

func pendingReviews() fakeReviews {
	return fakeReviews{rev: ReviewStatus{ID: laneReviewID, Status: "PENDING",
		ListenerName: "api", ApprovalRule: "payments", CreatedAt: time.Unix(0, 0).UTC()}}
}

func httpRequest(method, path string) inspect.Statement {
	return inspect.Statement{Protocol: inspect.HTTP, Direction: inspect.FromClient,
		HTTP: &inspect.HTTPDetail{Method: method, Path: path}}
}

func readLaneReply(t *testing.T, raw []byte, method string) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.ReadResponse(bufio.NewReader(bytes.NewReader(raw)), &http.Request{Method: method})
	if err != nil {
		t.Fatalf("not an HTTP response: %v\n%s", err, raw)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(body) > 0 {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("body is not JSON: %v\n%s", err, body)
		}
	}
	return resp, out
}

func TestTheReviewStatusPathAnswersFromThePlane(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	statusPath := ReviewStatusPath + laneReviewID
	for _, tc := range []struct {
		name     string
		reviews  ReviewStatusReader
		stmt     inspect.Statement
		code     int
		message  string
		passThru bool
	}{
		{name: "pending", reviews: pendingReviews(), stmt: httpRequest("GET", statusPath), code: 200},
		{name: "head", reviews: pendingReviews(), stmt: httpRequest("HEAD", statusPath), code: 200},
		{name: "unknown id", reviews: pendingReviews(),
			stmt: httpRequest("GET", ReviewStatusPath+"9f97"), code: 404, message: "review not found"},
		{name: "another lane's review", reviews: fakeReviews{rev: ReviewStatus{ID: laneReviewID,
			Status: "PENDING", ListenerName: "billing"}},
			stmt: httpRequest("GET", statusPath), code: 404, message: "review not found"},
		{name: "unrenderable plane answer", reviews: fakeReviews{rev: ReviewStatus{ID: laneReviewID,
			Status: "PENDING", ListenerName: "api", CreatedAt: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}},
			stmt: httpRequest("GET", statusPath), code: 500, message: "could not be read"},
		{name: "unserved route", reviews: pendingReviews(),
			stmt: httpRequest("GET", ReservedPathPrefix+"other"), code: 404, message: "not found"},
		{name: "write method", reviews: pendingReviews(),
			stmt: httpRequest("POST", statusPath), code: 405, message: "only GET and HEAD"},
		{name: "no plane", stmt: httpRequest("GET", statusPath), code: 503, message: "control plane"},
		{name: "old plane", reviews: fakeReviews{err: ErrPlaneTooOld},
			stmt: httpRequest("GET", statusPath), code: 502, message: "upgrade the control plane"},
		{name: "plane down", reviews: fakeReviews{err: errors.New("dial tcp 10.0.0.1:8009: refused")},
			stmt: httpRequest("GET", statusPath), code: 502, message: "could not report"},
		{name: "upstream route", reviews: pendingReviews(),
			stmt: httpRequest("GET", "/.well-known/openid-configuration"), passThru: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			answer := reviewStatusAnswer(ListenerConfig{Protocol: "http"}, "api", tc.reviews, quiet)
			reply := answer(tc.stmt)
			if tc.passThru {
				if reply != nil {
					t.Fatalf("the lane claimed a route it does not own: %s", tc.stmt.HTTP.Path)
				}
				return
			}
			raw := reply(context.Background())
			resp, body := readLaneReply(t, raw, tc.stmt.HTTP.Method)
			if resp.StatusCode != tc.code {
				t.Errorf("status = %d, want %d", resp.StatusCode, tc.code)
			}
			if !resp.Close {
				t.Error("the reply does not close the connection")
			}
			if tc.stmt.HTTP.Method == "HEAD" {
				if body != nil {
					t.Errorf("a HEAD reply carries a body: %v", body)
				}
				return
			}
			if tc.message != "" {
				if msg, _ := body["message"].(string); !strings.Contains(msg, tc.message) {
					t.Errorf("message = %q, want it to say %q", msg, tc.message)
				}
				if strings.Contains(body["message"].(string), "10.0.0.1") {
					t.Error("the reply names the plane's address")
				}
				return
			}
			want := map[string]any{"id": laneReviewID, "status": "PENDING", "listener_name": "api",
				"approval_rule": "payments", "next": ReviewNextWait}
			for k, v := range want {
				if body[k] != v {
					t.Errorf("%s = %v, want %v", k, body[k], v)
				}
			}
		})
	}
}

// Only http has a path to reserve.
func TestOnlyAnHTTPLaneReservesAPath(t *testing.T) {
	if reviewStatusAnswer(ListenerConfig{Protocol: "postgres"}, "api", pendingReviews(), slog.Default()) != nil {
		t.Error("a postgres lane answers a reserved path")
	}
}

// Through a lane built the way Run builds it: the status path is answered on
// the data port and never reaches the upstream.
func TestAnHTTPLaneServesReviewStatus(t *testing.T) {
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer upstream.Close()
	forwarded := make(chan struct{}, 1)
	go func() {
		for {
			c, aerr := upstream.Accept()
			if aerr != nil {
				return
			}
			go func() {
				defer c.Close()
				if n, _ := c.Read(make([]byte, 1)); n > 0 {
					forwarded <- struct{}{}
				}
			}()
		}
	}()

	ln := lane{name: "api", cfg: ListenerConfig{Name: "api", Protocol: "http",
		Listen: "127.0.0.1:0", Upstream: upstream.Addr().String()}}
	srv, err := buildServer(ln, AuditConfig{}, nil, pendingReviews(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = srv.Serve(ctx) }()
	addr := waitForAddr(t, srv)

	resp, err := http.Get("http://" + addr + ReviewStatusPath + laneReviewID)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.StatusCode != 200 || out["status"] != "PENDING" || out["next"] != ReviewNextWait {
		t.Errorf("status %d body %v, want 200 PENDING wait", resp.StatusCode, out)
	}
	select {
	case <-forwarded:
		t.Error("the upstream received the status request")
	case <-time.After(100 * time.Millisecond):
	}
}

func waitForAddr(t *testing.T, srv interface{ Addr() net.Addr }) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if a := srv.Addr(); a != nil {
			return a.String()
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the lane never bound")
	return ""
}

// The list is one GET under the reviews path, filtered by query, and a plane
// older than the route reads as too old, never as an empty list.
func TestListReviewsAsksThePlane(t *testing.T) {
	var gotQuery, gotPath string
	plane := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		_, _ = w.Write([]byte(`[{"id":"` + laneReviewID + `","status":"PENDING","listener_name":"api"}]`))
	}))
	defer plane.Close()
	cp := &controlPlane{url: plane.URL, token: "hsc_token"}

	revs, err := cp.ListReviews(context.Background(), "PENDING", 5)
	if err != nil {
		t.Fatalf("ListReviews: %v", err)
	}
	if gotPath != "/api/sidecars/reviews" || gotQuery != "limit=5&status=PENDING" {
		t.Errorf("asked %s?%s, want /api/sidecars/reviews?limit=5&status=PENDING", gotPath, gotQuery)
	}
	if len(revs) != 1 || revs[0].ID != laneReviewID {
		t.Errorf("reviews = %+v", revs)
	}
	if _, err := cp.ListReviews(context.Background(), "", 0); err == nil {
		t.Error("a zero limit reached the plane")
	}

	// An older plane answers either way: Gin's 404, or the admin
	// GET /sidecars/:name refusing a sidecar token.
	for _, code := range []int{http.StatusNotFound, http.StatusUnauthorized} {
		old, _ := reviewPlane(t, code, `{"message":"access denied"}`)
		if _, err := old.ListReviews(context.Background(), "", 5); !errors.Is(err, ErrPlaneTooOld) {
			t.Errorf("an old plane's %d read as %v, want ErrPlaneTooOld", code, err)
		}
	}
}
