package usertoken

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/hoophq/hoop/gateway/idp"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
)

const subject = "user-1"

var (
	errExpired   = fmt.Errorf("failed to parse access token: %w", jwt.ErrTokenExpired)
	errSignature = errors.New("signature is invalid")
	errDB        = errors.New("connection refused")
	errNoRefresh = errors.New("no refresh token available")
)

const expiredMsg = "access token is expired, try logging in again"

// fakeVerifier answers subject/err for the stored token and looks up
// refreshed tokens in fresh.
type fakeVerifier struct {
	subject string
	err     error
	fresh   map[string]string
}

func (f fakeVerifier) VerifyAccessToken(tok string) (string, error) {
	if sub, ok := f.fresh[tok]; ok {
		return sub, nil
	}
	return f.subject, f.err
}
func (f fakeVerifier) VerifyAccessTokenWithUserInfo(string) (*idptypes.ProviderUserInfo, error) {
	return nil, nil
}

var _ idp.UserInfoTokenVerifier = fakeVerifier{}

func activeUser() *models.Context {
	return &models.Context{OrgID: "org", UserSubject: subject, UserStatus: "active"}
}

func inactiveUser() *models.Context {
	return &models.Context{OrgID: "org", UserSubject: subject, UserStatus: "inactive"}
}

// stub replaces the model seams and returns a counter of token reads.
// The refresh seam fails until stubRefresh replaces it.
// Tests in this package must not run in parallel.
func stub(t *testing.T, userCtx *models.Context, userErr error, token *models.UserToken, tokenErr error) *int {
	t.Helper()
	prevCtx, prevTok, prevRefresh := getUserContext, getUserToken, refreshExpiredToken
	reads := new(int)
	getUserContext = func(string) (*models.Context, error) { return userCtx, userErr }
	getUserToken = func(string) (*models.UserToken, error) { *reads++; return token, tokenErr }
	refreshExpiredToken = func(idp.TokenVerifier, string) (string, string, error) {
		return "", "", errNoRefresh
	}
	t.Cleanup(func() { getUserContext, getUserToken, refreshExpiredToken = prevCtx, prevTok, prevRefresh })
	return reads
}

// stubRefresh makes the refresh return (subject, token, err) and counts calls.
func stubRefresh(t *testing.T, subject, token string, err error) *int {
	t.Helper()
	calls := new(int)
	refreshExpiredToken = func(_ idp.TokenVerifier, expired string) (string, string, error) {
		*calls++
		if expired != "t" {
			t.Errorf("refresh got %q, want the stored token", expired)
		}
		return subject, token, err
	}
	return calls
}

func TestCheckUserToken(t *testing.T) {
	tok := &models.UserToken{Token: "t"}
	tests := []struct {
		name       string
		userCtx    *models.Context
		userErr    error
		token      *models.UserToken
		tokenErr   error
		verifier   fakeVerifier
		refreshSub string
		refreshTok string
		refreshErr error
		wantErr    string
		wantErrIs  error
		wantNoRead bool
		wantCalls  int
	}{
		{name: "valid token", userCtx: activeUser(), token: tok, verifier: fakeVerifier{subject: subject}},
		{
			name: "expired token refreshed keeps the session", userCtx: activeUser(), token: tok,
			verifier:   fakeVerifier{err: errExpired, fresh: map[string]string{"new": subject}},
			refreshSub: subject, refreshTok: "new", wantCalls: 1,
		},
		{
			name: "expired token with failed refresh ends the session", userCtx: activeUser(), token: tok,
			verifier:   fakeVerifier{err: errExpired},
			refreshErr: errNoRefresh, wantErr: expiredMsg, wantCalls: 1,
		},
		{
			name: "refresh for another subject ends the session", userCtx: activeUser(), token: tok,
			verifier:   fakeVerifier{err: errExpired, fresh: map[string]string{"new": "other"}},
			refreshSub: "other", refreshTok: "new", wantErr: expiredMsg, wantCalls: 1,
		},
		{
			name: "refreshed token that does not verify ends the session", userCtx: activeUser(), token: tok,
			verifier:   fakeVerifier{err: errExpired},
			refreshSub: subject, refreshTok: "new", wantErr: expiredMsg, wantCalls: 1,
		},
		{
			name: "refreshed token for another subject ends the session", userCtx: activeUser(), token: tok,
			verifier:   fakeVerifier{err: errExpired, fresh: map[string]string{"new": "other"}},
			refreshSub: subject, refreshTok: "new", wantErr: expiredMsg, wantCalls: 1,
		},
		{name: "invalid signature", userCtx: activeUser(), token: tok, verifier: fakeVerifier{err: errSignature}, wantErrIs: errSignature},
		{name: "empty subject", userCtx: activeUser(), token: tok, wantErr: "user subject not found"},
		{name: "inactive user", userCtx: inactiveUser(), token: tok, wantErr: "user is not active", wantNoRead: true},
		{name: "user not found", userCtx: &models.Context{}, token: tok, wantErr: "user not found", wantNoRead: true},
		{name: "user lookup error", userCtx: &models.Context{}, userErr: errDB, wantErrIs: errDB, wantNoRead: true},
		{name: "token row missing", userCtx: activeUser(), wantErr: "access token not found"},
		{name: "token lookup error", userCtx: activeUser(), tokenErr: errDB, wantErrIs: errDB},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reads := stub(t, tt.userCtx, tt.userErr, tt.token, tt.tokenErr)
			calls := stubRefresh(t, tt.refreshSub, tt.refreshTok, tt.refreshErr)
			err := CheckUserToken(tt.verifier, subject)
			switch {
			case tt.wantErrIs != nil:
				if !errors.Is(err, tt.wantErrIs) {
					t.Fatalf("want %v, got %v", tt.wantErrIs, err)
				}
			case tt.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
				}
			default:
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			}
			if tt.wantNoRead && *reads != 0 {
				t.Fatal("token read for a user that failed the status check")
			}
			if *calls != tt.wantCalls {
				t.Fatalf("want %d refresh calls, got %d", tt.wantCalls, *calls)
			}
		})
	}
}

func startPoller(t *testing.T, v fakeVerifier, interval, maxAge time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancelCause(context.Background())
	done := pollUserToken(ctx, cancel, v, subject, interval, maxAge)
	t.Cleanup(func() {
		cancel(context.Canceled)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("poller did not stop")
		}
	})
	return ctx
}

func waitCancelled(t *testing.T, ctx context.Context) error {
	t.Helper()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-time.After(5 * time.Second):
		t.Fatal("session was not cancelled")
		return nil
	}
}

func TestPollUserToken_RefreshedTokenKeepsSession(t *testing.T) {
	stub(t, activeUser(), nil, &models.UserToken{Token: "t"}, nil)
	stubRefresh(t, subject, "new", nil)
	v := fakeVerifier{err: errExpired, fresh: map[string]string{"new": subject}}
	ctx := startPoller(t, v, time.Millisecond, time.Hour)
	select {
	case <-ctx.Done():
		t.Fatalf("session ended: %v", context.Cause(ctx))
	case <-time.After(50 * time.Millisecond):
	}
}

func TestPollUserToken_FailedRefreshEndsSession(t *testing.T) {
	stub(t, activeUser(), nil, &models.UserToken{Token: "t"}, nil)
	ctx := startPoller(t, fakeVerifier{err: errExpired}, time.Millisecond, time.Hour)
	if got := waitCancelled(t, ctx); !strings.Contains(got.Error(), expiredMsg) {
		t.Fatalf("unexpected cause: %v", got)
	}
}

func TestPollUserToken_InactiveUserEndsSession(t *testing.T) {
	stub(t, inactiveUser(), nil, nil, nil)
	ctx := startPoller(t, fakeVerifier{subject: subject}, time.Millisecond, time.Hour)
	if got := waitCancelled(t, ctx); !strings.Contains(got.Error(), "user is not active") {
		t.Fatalf("unexpected cause: %v", got)
	}
}

func TestPollUserToken_MaxAgeEndsSession(t *testing.T) {
	stub(t, activeUser(), nil, &models.UserToken{Token: "t"}, nil)
	ctx := startPoller(t, fakeVerifier{subject: subject}, time.Hour, time.Millisecond)
	if got := waitCancelled(t, ctx); !strings.Contains(got.Error(), "maximum duration") {
		t.Fatalf("unexpected cause: %v", got)
	}
}

func TestPollUserToken_RefusesBackgroundContext(t *testing.T) {
	var cause error
	done := pollUserToken(context.Background(), func(err error) { cause = err }, fakeVerifier{}, subject, time.Hour, time.Hour)
	<-done
	if cause == nil || !strings.Contains(cause.Error(), "cancellable") {
		t.Fatalf("unexpected cause: %v", cause)
	}
}

func TestPollUserToken_StopsWithContext(t *testing.T) {
	stub(t, activeUser(), nil, &models.UserToken{Token: "t"}, nil)
	ctx, cancel := context.WithCancelCause(context.Background())
	done := pollUserToken(ctx, cancel, fakeVerifier{subject: subject}, subject, time.Millisecond, time.Hour)
	cancel(context.Canceled)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("poller did not stop")
	}
}
