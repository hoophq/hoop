//go:build scenario && browser

package scenario

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// TestBrowserLogin logs in to the hoop web UI through the OIDC twin's login
// page, in a headless Chrome, and saves a screenshot of each step to
// <TWINS_ARTIFACTS>/screenshots. `make test-twins-browser` runs it.
func TestBrowserLogin(t *testing.T) {
	s := newStack(t, stackOpts{ui: true})
	seedOIDC(t, map[string]any{"users": []map[string]any{
		{"sub": "bob-sub", "email": "bob@example.com", "name": "Bob", "groups": []string{"admin", "sre"}},
		{"sub": "carol-sub", "email": "carol@example.com", "name": "Carol", "groups": []string{"sre"}},
	}})
	b := newBrowser(t, s)

	// A tab on the twin's page stays open for the whole flow: it must show
	// alice's login with no reload.
	twinTab := b.tab(t)
	b.run(t, twinTab, "open the OIDC twin's page", chromedp.Navigate(oidcAdmin+"/_twin/ui"),
		chromedp.WaitVisible(`section[data-section="users"] td`, chromedp.ByQuery))
	tokensBefore := b.count(t, twinTab, `section[data-section="tokens"] tbody tr`)
	b.shot(t, twinTab, "01-twin-state-before")

	hoop := b.tab(t)
	b.run(t, hoop, "open hoop: it sends the browser to the twin's login page",
		chromedp.Navigate(apiURL+"/"),
		chromedp.WaitVisible(`button[data-user="alice@example.com"]`, chromedp.ByQuery))
	if loc := b.location(t, hoop); !strings.HasPrefix(loc, oidcBrowser+"/authorize?") {
		t.Fatalf("hoop sent the browser to %s, want the twin's authorize endpoint", loc)
	}
	b.shot(t, hoop, "02-twin-login-page")

	b.run(t, hoop, "pick alice", chromedp.Click(`button[data-user="alice@example.com"]`, chromedp.ByQuery))
	// The twin redirects to the gateway's callback, which sets the token
	// cookie and redirects to the web UI's callback, which stores the token
	// and opens the user's landing page.
	token := b.waitFor(t, hoop, "hoop to sign alice in", 60*time.Second, `(() => {
		const p = location.pathname;
		if (location.origin !== `+strconv.Quote(apiURL)+` || p.startsWith('/auth') || p.startsWith('/login')) return '';
		return localStorage.getItem('jwt-token') || '';
	})()`)
	if me := userinfo(t, token); me.Email != firstUserEmail || !me.IsAdmin {
		t.Fatalf("the web UI holds a token of %+v, want alice as admin", me)
	}
	// The user menu names who is signed in.
	b.run(t, hoop, "open the user menu",
		chromedp.WaitVisible(`button[aria-label="Open user menu"]`, chromedp.ByQuery),
		chromedp.Click(`button[aria-label="Open user menu"]`, chromedp.ByQuery))
	// Opaque: the menu fades in, and the screenshot comes next.
	b.waitFor(t, hoop, "the user menu to show alice", 10*time.Second, `(() => {
		const m = document.querySelector('[role="menu"]');
		return m && m.innerText.includes(`+strconv.Quote(firstUserEmail)+`) && getComputedStyle(m).opacity === '1' ? 'yes' : '';
	})()`)
	b.shot(t, hoop, "03-hoop-as-alice")
	s.mark(t, "browser: picked alice on the twin's page, hoop signed her in")

	// The twin's tab refreshed itself: it shows the code and the tokens of
	// the login that just happened.
	b.waitFor(t, twinTab, "the twin's page to show alice's new tokens", 10*time.Second,
		fmt.Sprintf(`document.querySelectorAll('section[data-section="tokens"] tbody tr').length >= %d ? 'yes' : ''`, tokensBefore+3))
	b.shot(t, twinTab, "04-twin-state-after")
	s.mark(t, "browser: the twin's page refreshed itself with alice's tokens")
}

// browser is a headless Chrome. Each tab is a context of it.
type browser struct {
	ctx   context.Context
	shots string
}

func newBrowser(t *testing.T, s *stack) *browser {
	t.Helper()
	opts := append(chromedp.DefaultExecAllocatorOptions[:], chromedp.WindowSize(1280, 800))
	if runtime.GOOS == "linux" {
		// Ubuntu 24.04 runners refuse the user namespaces of Chrome's sandbox.
		opts = append(opts, chromedp.NoSandbox)
	}
	if bin := os.Getenv("TWINS_CHROME"); bin != "" {
		opts = append(opts, chromedp.ExecPath(bin))
	}
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(alloc)
	t.Cleanup(func() { cancel(); cancelAlloc() })
	// The first run starts the browser.
	if err := chromedp.Run(ctx); err != nil {
		t.Fatalf("start Chrome (set TWINS_CHROME to its binary): %v", err)
	}
	shots := filepath.Join(s.artifacts, "screenshots")
	if err := os.MkdirAll(shots, 0o755); err != nil {
		t.Fatal(err)
	}
	return &browser{ctx: ctx, shots: shots}
}

// tab opens a tab in the browser.
func (b *browser) tab(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := chromedp.NewContext(b.ctx)
	t.Cleanup(cancel)
	if err := chromedp.Run(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx
}

// run runs the step's actions and fails the scenario with the step name.
// Chrome throttles the rendering of a tab in the background, so every step
// brings its tab to the front first.
func (b *browser) run(t *testing.T, tab context.Context, step string, actions ...chromedp.Action) {
	t.Helper()
	ctx, cancel := context.WithTimeout(tab, 90*time.Second)
	defer cancel()
	if err := chromedp.Run(ctx, append([]chromedp.Action{page.BringToFront()}, actions...)...); err != nil {
		b.shot(t, tab, "failed-"+strings.ReplaceAll(step, " ", "-"))
		t.Fatalf("%s: %v (at %s)", step, err, b.location(t, tab))
	}
}

// waitFor evaluates js until it returns a non-empty string, and returns it.
// An evaluation that a navigation interrupts counts as not yet: a page
// that redirects destroys the context the script runs in.
func (b *browser) waitFor(t *testing.T, tab context.Context, what string, timeout time.Duration, js string) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		var out string
		ctx, cancel := context.WithTimeout(tab, 5*time.Second)
		lastErr = chromedp.Run(ctx, page.BringToFront(), chromedp.Evaluate(js, &out))
		cancel()
		if lastErr == nil && out != "" {
			return out
		}
		time.Sleep(200 * time.Millisecond)
	}
	b.shot(t, tab, "failed-"+strings.ReplaceAll(what, " ", "-"))
	t.Fatalf("timed out after %s waiting for %s (at %s, last error: %v)", timeout, what, b.location(t, tab), lastErr)
	return ""
}

func (b *browser) shot(t *testing.T, tab context.Context, name string) {
	t.Helper()
	var png []byte
	ctx, cancel := context.WithTimeout(tab, 20*time.Second)
	defer cancel()
	// The whole page, so a twin's lower sections are in it. Quality 100 is PNG.
	if err := chromedp.Run(ctx, page.BringToFront(), chromedp.FullScreenshot(&png, 100)); err != nil {
		t.Errorf("screenshot %s: %v", name, err)
		return
	}
	if err := os.WriteFile(filepath.Join(b.shots, name+".png"), png, 0o644); err != nil {
		t.Errorf("screenshot %s: %v", name, err)
	}
}

func (b *browser) count(t *testing.T, tab context.Context, selector string) int {
	t.Helper()
	var n int
	b.run(t, tab, "count "+selector, chromedp.Evaluate(fmt.Sprintf(`document.querySelectorAll(%q).length`, selector), &n))
	return n
}

func (b *browser) location(t *testing.T, tab context.Context) string {
	t.Helper()
	var loc string
	ctx, cancel := context.WithTimeout(tab, 10*time.Second)
	defer cancel()
	_ = chromedp.Run(ctx, chromedp.Location(&loc))
	if u, err := url.Parse(loc); err == nil {
		return u.Redacted()
	}
	return loc
}
