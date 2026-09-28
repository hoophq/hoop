package google

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

// tokeninfo is a fake Google tokeninfo endpoint. It fails the test if the
// token arrives anywhere but the form body, and counts the calls that reach it.
type tokeninfo struct {
	srv  *httptest.Server
	hits atomic.Int64
}

func newTokeninfo(t *testing.T, answer func(w http.ResponseWriter, token string)) *tokeninfo {
	t.Helper()
	ti := &tokeninfo{}
	ti.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ti.hits.Add(1)
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("Content-Type = %q", ct)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}
		token := r.PostForm.Get("access_token")
		if token == "" {
			t.Errorf("form body has no access_token")
		}
		if r.RequestURI != "/tokeninfo" {
			t.Errorf("request URI = %q, want exactly the configured path", r.RequestURI)
		}
		answer(w, token)
	}))
	t.Cleanup(ti.srv.Close)
	return ti
}

func (ti *tokeninfo) resolver(t *testing.T, o Options) *Resolver {
	t.Helper()
	// Exactly this path is what the server accepts: the token must not ride
	// in the URL, which every proxy on the path logs.
	o.TokenInfoURL = ti.srv.URL + "/tokeninfo"
	if o.Client == nil {
		o.Client = ti.srv.Client()
	}
	r, err := New(o)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func reply(status int, body string) func(http.ResponseWriter, string) {
	return func(w http.ResponseWriter, _ string) {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}
}

func valid(exp time.Time, extra string) func(http.ResponseWriter, string) {
	return reply(http.StatusOK, fmt.Sprintf(`{"sub":"1234567890","azp":"32555940559.apps.googleusercontent.com",
		"aud":"32555940559.apps.googleusercontent.com","scope":"openid https://www.googleapis.com/auth/cloud-platform",
		"exp":"%d","expires_in":"3599"%s}`, exp.Unix(), extra))
}

// Google's observed rejection, verbatim.
const invalidTokenBody = `{"error": "invalid_token", "error_description": "Invalid Value"}`

func TestResolveVerifiedEmail(t *testing.T) {
	clk := &clock{t: t0}
	ti := newTokeninfo(t, valid(t0.Add(time.Hour), `,"email":"dev@example.com","email_verified":"true"`))
	r := ti.resolver(t, Options{Now: clk.Now})

	id, err := r.Resolve(context.Background(), "Bearer ya29.a0Af-valid_token")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if id.Subject != "dev@example.com" || id.Email != "dev@example.com" {
		t.Errorf("Subject/Email = %q/%q, want the verified email", id.Subject, id.Email)
	}
	if id.Attributes["google.sub"] != "1234567890" || id.Attributes["google.azp"] != "32555940559.apps.googleusercontent.com" {
		t.Errorf("Attributes = %v", id.Attributes)
	}
}

func TestResolveUnverifiedEmailFallsBackToSub(t *testing.T) {
	for _, verified := range []string{`"false"`, `false`} {
		t.Run(verified, func(t *testing.T) {
			ti := newTokeninfo(t, valid(t0.Add(time.Hour), `,"email":"typed@example.com","email_verified":`+verified))
			r := ti.resolver(t, Options{Now: (&clock{t: t0}).Now})
			id, err := r.Resolve(context.Background(), "Bearer tok")
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if id.Subject != "1234567890" || id.Email != "" {
				t.Errorf("Subject/Email = %q/%q, want sub and no email", id.Subject, id.Email)
			}
		})
	}
}

func TestResolveBoolVerifiedAccepted(t *testing.T) {
	ti := newTokeninfo(t, valid(t0.Add(time.Hour), `,"email":"dev@example.com","email_verified":true`))
	r := ti.resolver(t, Options{Now: (&clock{t: t0}).Now})
	id, err := r.Resolve(context.Background(), "Bearer tok")
	if err != nil || id.Email != "dev@example.com" {
		t.Fatalf("Resolve = %+v, %v", id, err)
	}
}

func TestResolveNamesNobodyRefused(t *testing.T) {
	ti := newTokeninfo(t, reply(http.StatusOK, fmt.Sprintf(
		`{"email":"typed@example.com","email_verified":"false","exp":"%d"}`, t0.Add(time.Hour).Unix())))
	r := ti.resolver(t, Options{Now: (&clock{t: t0}).Now})
	if _, err := r.Resolve(context.Background(), "Bearer tok"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestBearerParsing(t *testing.T) {
	ti := newTokeninfo(t, valid(t0.Add(time.Hour), ""))
	r := ti.resolver(t, Options{Now: (&clock{t: t0}).Now})

	for _, tc := range []struct {
		credential string
		want       error
	}{
		{"", ErrNoToken},
		{"   ", ErrNoToken},
		{"ya29.bare", ErrInvalidToken},
		{"Basic dXNlcjpwYXNz", ErrInvalidToken},
		{"Token ya29.x", ErrInvalidToken},
		{"Bearer", ErrInvalidToken},
		{"Bearer ", ErrInvalidToken},
		{"Bearer a b", ErrInvalidToken},
		{"Bearer ya29.one, Bearer ya29.two", ErrInvalidToken}, // two headers joined
		{"Bearer ya29.one,ya29.two", ErrInvalidToken},
		{"Bearer tok\x00", ErrInvalidToken},
		{"bearer ya29.lower", nil},
		{"BEARER  ya29.two-spaces", nil},
		{"Bearer ya29.padded==", nil},
	} {
		_, err := r.Resolve(context.Background(), tc.credential)
		if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("Resolve(%q) err = %v, want %v", tc.credential, err, tc.want)
		}
	}
	if got := ti.hits.Load(); got != 3 {
		t.Errorf("tokeninfo hits = %d, want 3 (malformed credentials must not reach Google)", got)
	}
}

func TestInvalidTokenNegativeCached(t *testing.T) {
	clk := &clock{t: t0}
	ti := newTokeninfo(t, reply(http.StatusBadRequest, invalidTokenBody))
	r := ti.resolver(t, Options{Now: clk.Now})

	for range 5 {
		if _, err := r.Resolve(context.Background(), "Bearer dead"); !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("err = %v, want ErrInvalidToken", err)
		}
	}
	if got := ti.hits.Load(); got != 1 {
		t.Fatalf("hits = %d, want 1 (rejection must be remembered)", got)
	}
	clk.Advance(negativeTTL - time.Second)
	r.Resolve(context.Background(), "Bearer dead")
	if got := ti.hits.Load(); got != 1 {
		t.Fatalf("hits after %s = %d, want 1", negativeTTL-time.Second, got)
	}
	clk.Advance(time.Second)
	if _, err := r.Resolve(context.Background(), "Bearer dead"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v", err)
	}
	if got := ti.hits.Load(); got != 2 {
		t.Fatalf("hits after %s = %d, want 2 (negative entry must expire)", negativeTTL, got)
	}
}

func TestUnauthorizedIsInvalid(t *testing.T) {
	ti := newTokeninfo(t, reply(http.StatusUnauthorized, ""))
	r := ti.resolver(t, Options{})
	if _, err := r.Resolve(context.Background(), "Bearer dead"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestUnavailableNotCached(t *testing.T) {
	for name, answer := range map[string]func(http.ResponseWriter, string){
		"500":          reply(http.StatusInternalServerError, `{"error":"backend"}`),
		"503":          reply(http.StatusServiceUnavailable, ""),
		"429":          reply(http.StatusTooManyRequests, ""),
		"404":          reply(http.StatusNotFound, ""),
		"undecodable":  reply(http.StatusOK, `<html>captive portal</html>`),
		"bad exp":      reply(http.StatusOK, `{"sub":"1","exp":"soon"}`),
		"no exp":       reply(http.StatusOK, `{"sub":"1"}`),
		"bad verified": reply(http.StatusOK, `{"sub":"1","exp":"9999999999","email_verified":"maybe"}`),
	} {
		t.Run(name, func(t *testing.T) {
			ti := newTokeninfo(t, answer)
			r := ti.resolver(t, Options{Now: (&clock{t: t0}).Now})
			for i := range 2 {
				if _, err := r.Resolve(context.Background(), "Bearer tok"); !errors.Is(err, ErrUnavailable) {
					t.Fatalf("call %d err = %v, want ErrUnavailable", i, err)
				}
			}
			if got := ti.hits.Load(); got != 2 {
				t.Fatalf("hits = %d, want 2 (an unavailable Google must not be cached)", got)
			}
		})
	}
}

// countingTransport counts attempts that never reach a server.
type countingTransport struct {
	next  http.RoundTripper
	calls atomic.Int64
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return c.next.RoundTrip(r)
}

func TestConnectionRefusedUnavailableNotCached(t *testing.T) {
	ti := newTokeninfo(t, valid(t0.Add(time.Hour), ""))
	ct := &countingTransport{next: ti.srv.Client().Transport}
	r := ti.resolver(t, Options{Client: &http.Client{Transport: ct}})
	ti.srv.Close()

	for i := range 2 {
		if _, err := r.Resolve(context.Background(), "Bearer tok"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("call %d err = %v, want ErrUnavailable", i, err)
		}
	}
	if got := ct.calls.Load(); got != 2 {
		t.Fatalf("attempts = %d, want 2", got)
	}
}

func TestSuccessCachedUntilMaxTTL(t *testing.T) {
	clk := &clock{t: t0}
	ti := newTokeninfo(t, valid(t0.Add(time.Hour), `,"email":"dev@example.com","email_verified":"true"`))
	r := ti.resolver(t, Options{Now: clk.Now, MaxCacheTTL: 5 * time.Minute})

	for range 20 {
		if _, err := r.Resolve(context.Background(), "Bearer tok"); err != nil {
			t.Fatal(err)
		}
	}
	clk.Advance(5*time.Minute - time.Second)
	r.Resolve(context.Background(), "Bearer tok")
	if got := ti.hits.Load(); got != 1 {
		t.Fatalf("hits = %d, want 1 within MaxCacheTTL", got)
	}
	clk.Advance(time.Second)
	if _, err := r.Resolve(context.Background(), "Bearer tok"); err != nil {
		t.Fatal(err)
	}
	if got := ti.hits.Load(); got != 2 {
		t.Fatalf("hits = %d, want 2 once MaxCacheTTL elapsed", got)
	}
}

// A token expiring before MaxCacheTTL stops resolving at its exp, not later.
func TestSuccessCachedUntilExp(t *testing.T) {
	clk := &clock{t: t0}
	ti := newTokeninfo(t, valid(t0.Add(time.Minute), ""))
	r := ti.resolver(t, Options{Now: clk.Now, MaxCacheTTL: time.Hour})

	if _, err := r.Resolve(context.Background(), "Bearer tok"); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Minute - time.Second)
	if _, err := r.Resolve(context.Background(), "Bearer tok"); err != nil {
		t.Fatal(err)
	}
	if got := ti.hits.Load(); got != 1 {
		t.Fatalf("hits = %d, want 1 before exp", got)
	}
	clk.Advance(time.Second)
	if _, err := r.Resolve(context.Background(), "Bearer tok"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err at exp = %v, want ErrInvalidToken", err)
	}
	if got := ti.hits.Load(); got != 2 {
		t.Fatalf("hits = %d, want 2 (entry must expire at exp)", got)
	}
}

func TestExpiredTokenRefused(t *testing.T) {
	ti := newTokeninfo(t, valid(t0.Add(-time.Second), `,"email":"dev@example.com","email_verified":"true"`))
	r := ti.resolver(t, Options{Now: (&clock{t: t0}).Now})
	if _, err := r.Resolve(context.Background(), "Bearer tok"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestErrorsNeverContainToken(t *testing.T) {
	const token = "ya29.SECRET+tok/en~value=="
	leaks := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("want an error")
		}
		for _, s := range []string{token, url.QueryEscape(token), "SECRET"} {
			if strings.Contains(err.Error(), s) {
				t.Errorf("error %q contains %q", err.Error(), s)
			}
		}
	}
	echo := func(status int) func(http.ResponseWriter, string) {
		return func(w http.ResponseWriter, got string) {
			w.WriteHeader(status)
			fmt.Fprintf(w, `{"error":"invalid_token %s","error_description":"bad %s"}`, got, url.QueryEscape(got))
		}
	}
	for name, answer := range map[string]func(http.ResponseWriter, string){
		"400 echoing": echo(http.StatusBadRequest),
		"500 echoing": echo(http.StatusInternalServerError),
		"200 echoing": func(w http.ResponseWriter, got string) { fmt.Fprintf(w, `{"sub":"1","exp":%q}`, got) },
		"expired":     valid(t0.Add(-time.Hour), ""),
	} {
		t.Run(name, func(t *testing.T) {
			ti := newTokeninfo(t, answer)
			r := ti.resolver(t, Options{Now: (&clock{t: t0}).Now})
			_, err := r.Resolve(context.Background(), "Bearer "+token)
			leaks(t, err)
		})
	}
	t.Run("refused", func(t *testing.T) {
		ti := newTokeninfo(t, valid(t0.Add(time.Hour), ""))
		r := ti.resolver(t, Options{})
		ti.srv.Close()
		_, err := r.Resolve(context.Background(), "Bearer "+token)
		leaks(t, err)
	})
	t.Run("malformed", func(t *testing.T) {
		r, err := New(Options{})
		if err != nil {
			t.Fatal(err)
		}
		for _, cred := range []string{"Bearer " + token + ", Bearer x", "Basic " + token, "Bearer " + token + " extra"} {
			_, err := r.Resolve(context.Background(), cred)
			leaks(t, err)
		}
	})
}

func TestConcurrentSameTokenOneCall(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	answer := valid(t0.Add(time.Hour), `,"email":"dev@example.com","email_verified":"true"`)
	ti := newTokeninfo(t, func(w http.ResponseWriter, token string) {
		entered <- struct{}{}
		<-release
		answer(w, token)
	})
	r := ti.resolver(t, Options{Now: (&clock{t: t0}).Now})

	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	call := func() {
		defer wg.Done()
		id, err := r.Resolve(context.Background(), "Bearer tok")
		if err == nil && id.Subject != "dev@example.com" {
			err = fmt.Errorf("Subject = %q", id.Subject)
		}
		errs <- err
	}
	wg.Add(1)
	go call()
	<-entered // the first call is now in flight and held
	for range n - 1 {
		wg.Add(1)
		go call()
	}
	time.Sleep(50 * time.Millisecond) // let the followers join the flight
	close(release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if got := ti.hits.Load(); got != 1 {
		t.Fatalf("hits = %d, want 1 for %d concurrent lookups of one token", got, n)
	}
}

// The caller that starts a shared lookup hanging up must not fail the others.
func TestLeaderCancelDoesNotFailFollowers(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	answer := valid(t0.Add(time.Hour), "")
	ti := newTokeninfo(t, func(w http.ResponseWriter, token string) {
		entered <- struct{}{}
		<-release
		answer(w, token)
	})
	r := ti.resolver(t, Options{Now: (&clock{t: t0}).Now})

	ctx, cancel := context.WithCancel(context.Background())
	leader := make(chan error, 1)
	go func() {
		_, err := r.Resolve(ctx, "Bearer tok")
		leader <- err
	}()
	<-entered
	follower := make(chan error, 1)
	go func() {
		_, err := r.Resolve(context.Background(), "Bearer tok")
		follower <- err
	}()
	cancel()
	if err := <-leader; !errors.Is(err, ErrUnavailable) {
		t.Fatalf("canceled leader err = %v, want ErrUnavailable", err)
	}
	close(release)
	if err := <-follower; err != nil {
		t.Fatalf("follower err = %v, want the shared answer", err)
	}
	if got := ti.hits.Load(); got != 1 {
		t.Fatalf("hits = %d, want 1", got)
	}
}

// A redirect would replay the POST body, which is the token, to Location.
func TestRedirectNotFollowed(t *testing.T) {
	var elsewhere atomic.Int64
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		elsewhere.Add(1)
	}))
	defer other.Close()
	ti := newTokeninfo(t, func(w http.ResponseWriter, _ string) {
		w.Header().Set("Location", other.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	})
	r := ti.resolver(t, Options{})
	if _, err := r.Resolve(context.Background(), "Bearer tok"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if got := elsewhere.Load(); got != 0 {
		t.Fatalf("redirect target received %d requests", got)
	}
}

func TestEvictionDropsExpiredFirst(t *testing.T) {
	clk := &clock{t: t0}
	ti := newTokeninfo(t, func(w http.ResponseWriter, token string) {
		exp := t0.Add(time.Hour)
		if token == "short" {
			exp = t0.Add(time.Minute)
		}
		valid(exp, "")(w, token)
	})
	r := ti.resolver(t, Options{Now: clk.Now, CacheSize: 2, MaxCacheTTL: time.Hour})

	for _, tok := range []string{"short", "long"} {
		if _, err := r.Resolve(context.Background(), "Bearer "+tok); err != nil {
			t.Fatal(err)
		}
	}
	clk.Advance(2 * time.Minute) // "short" is now expired but still resident
	if _, err := r.Resolve(context.Background(), "Bearer third"); err != nil {
		t.Fatal(err)
	}
	before := ti.hits.Load()
	if _, err := r.Resolve(context.Background(), "Bearer long"); err != nil {
		t.Fatal(err)
	}
	if got := ti.hits.Load(); got != before {
		t.Fatalf("live entry evicted while an expired one was available")
	}
}

func TestCacheBounded(t *testing.T) {
	ti := newTokeninfo(t, valid(t0.Add(time.Hour), ""))
	r := ti.resolver(t, Options{Now: (&clock{t: t0}).Now, CacheSize: 3})
	for i := range 10 {
		if _, err := r.Resolve(context.Background(), fmt.Sprintf("Bearer tok%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	r.mu.Lock()
	n := len(r.entries)
	r.mu.Unlock()
	if n > 3 {
		t.Fatalf("cache holds %d entries, bound is 3", n)
	}
}

func TestNewValidates(t *testing.T) {
	for _, o := range []Options{
		{TokenInfoURL: "http://oauth2.googleapis.com/tokeninfo"},
		{TokenInfoURL: "http://127.0.0.1:8080/tokeninfo"},
		{TokenInfoURL: "/tokeninfo"},
		{TokenInfoURL: "oauth2.googleapis.com/tokeninfo"},
		{TokenInfoURL: "https://user:pass@oauth2.googleapis.com/tokeninfo"},
		{CacheSize: -1},
		{MaxCacheTTL: -time.Second},
	} {
		if _, err := New(o); err == nil {
			t.Errorf("New(%+v) accepted", o)
		}
	}
	if _, err := New(Options{}); err != nil {
		t.Errorf("New(zero) = %v", err)
	}
}

// Mutating a returned identity must not rewrite the cached one.
func TestReturnedIdentityIsACopy(t *testing.T) {
	ti := newTokeninfo(t, valid(t0.Add(time.Hour), ""))
	r := ti.resolver(t, Options{Now: (&clock{t: t0}).Now})
	id, err := r.Resolve(context.Background(), "Bearer tok")
	if err != nil {
		t.Fatal(err)
	}
	id.Attributes["google.sub"] = "someone-else"
	again, _ := r.Resolve(context.Background(), "Bearer tok")
	if again.Attributes["google.sub"] != "1234567890" {
		t.Fatalf("cached identity was mutated through a returned copy")
	}
}
