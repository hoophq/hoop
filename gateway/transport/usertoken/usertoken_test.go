package usertoken

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/hoophq/hoop/gateway/idp"
	idptypes "github.com/hoophq/hoop/gateway/idp/types"
	"github.com/hoophq/hoop/gateway/models"
)

const (
	testSubject       = "subject-1"
	storedExpired     = "expired-access-token"
	validToken        = "valid-access-token"
	badToken          = "bad-access-token"
	noSubjectToken    = "no-subject-access-token"
	otherSubjectToken = "other-subject-access-token"
)

// errExpired is built from the upstream sentinel, so a golang-jwt reword
// fails this test instead of silently breaking the strings.Contains match.
var (
	errExpired   = fmt.Errorf("failed to parse access token: token has invalid claims: %v", jwt.ErrTokenExpired)
	errDatabase  = errors.New("dial tcp: connection refused")
	errSignature = errors.New("failed to verify the token signature")
	errNoRefresh = errors.New("no refresh token available")
)

type fakeVerifier struct {
	verifyFn func(accessToken string) (string, error)
}

func (f *fakeVerifier) VerifyAccessToken(accessToken string) (string, error) {
	return f.verifyFn(accessToken)
}

func (f *fakeVerifier) VerifyAccessTokenWithUserInfo(string) (*idptypes.ProviderUserInfo, error) {
	return &idptypes.ProviderUserInfo{Subject: testSubject}, nil
}

func rejectExpired(accessToken string) (string, error) {
	switch accessToken {
	case storedExpired:
		return "", errExpired
	case badToken:
		return "", errSignature
	case noSubjectToken:
		return "", nil
	case otherSubjectToken:
		return "another-subject", nil
	}
	return testSubject, nil
}

// startPolling hangs off seams so stubSeams is the way in. Keep it a method,
// and keep stubSeams the only constructor.
type seams struct{ t *testing.T }

// stubSeams puts the package variables back after the test. Tests in this file
// must not call t.Parallel: the seams are package level.
func stubSeams(t *testing.T) *seams {
	t.Helper()
	load, refresh := loadUserToken, idpRefreshExpiredToken
	loadUserToken = func(string) (*models.UserToken, error) {
		panic("the test must replace loadUserToken")
	}
	idpRefreshExpiredToken = func(idp.TokenVerifier, string) (string, string, error) {
		panic("the test must replace idpRefreshExpiredToken")
	}
	t.Cleanup(func() { loadUserToken, idpRefreshExpiredToken = load, refresh })
	return &seams{t: t}
}

// startPolling drives the poller at 1ms. It waits for the goroutine to stop
// before the seams go back, so the poller never reads a variable under change.
func (s *seams) startPolling(verifyFn func(string) (string, error)) (context.Context, chan error) {
	s.t.Helper()
	ctx, cancel := context.WithCancelCause(context.Background())
	cancelled := make(chan error, 1)
	stopped := pollUserToken(ctx, func(cause error) {
		select {
		case cancelled <- cause:
		default:
		}
		cancel(cause)
	}, &fakeVerifier{verifyFn: verifyFn}, testSubject, time.Millisecond)
	s.t.Cleanup(func() {
		cancel(context.Canceled)
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			s.t.Error("the poller goroutine did not stop")
		}
	})
	return ctx, cancelled
}

func TestCheckUserToken(t *testing.T) {
	for _, tt := range []struct {
		name           string
		userToken      *models.UserToken
		getErr         error
		verifyFn       func(string) (string, error)
		refreshSubject string
		refreshToken   string
		refreshErr     error
		wantErr        string
		wantErrIs      error
		wantRefresh    int64
	}{
		{
			name:      "a valid token does not start a refresh",
			userToken: &models.UserToken{Token: validToken},
		},
		{
			name:           "an expired token continues after a good refresh",
			userToken:      &models.UserToken{Token: storedExpired},
			refreshSubject: testSubject,
			wantRefresh:    1,
		},
		{
			name:        "an expired token ends the session when the refresh fails",
			userToken:   &models.UserToken{Token: storedExpired},
			refreshErr:  errNoRefresh,
			wantErr:     "access token is expired, try logging in again",
			wantRefresh: 1,
		},
		{
			name:           "a refreshed token that does not verify ends the session",
			userToken:      &models.UserToken{Token: storedExpired},
			refreshSubject: testSubject,
			refreshToken:   badToken,
			wantErr:        "access token is expired, try logging in again",
			wantRefresh:    1,
		},
		{
			name:           "a refreshed token with no subject ends the session",
			userToken:      &models.UserToken{Token: storedExpired},
			refreshSubject: testSubject,
			refreshToken:   noSubjectToken,
			wantErr:        "access token is expired, try logging in again",
			wantRefresh:    1,
		},
		{
			name:           "a refreshed token for another subject ends the session",
			userToken:      &models.UserToken{Token: storedExpired},
			refreshSubject: testSubject,
			refreshToken:   otherSubjectToken,
			wantErr:        "access token is expired, try logging in again",
			wantRefresh:    1,
		},
		{
			name:           "a refresh for a different subject ends the session",
			userToken:      &models.UserToken{Token: storedExpired},
			refreshSubject: "another-subject",
			wantErr:        "access token is expired, try logging in again",
			wantRefresh:    1,
		},
		{
			// models.GetUserToken gives (nil, nil) when it finds no row.
			name:    "no token record for the user",
			wantErr: "access token not found for user subject",
		},
		{
			name:      "a database error goes to the caller",
			getErr:    errDatabase,
			wantErrIs: errDatabase,
		},
		{
			name:      "an empty subject ends the session",
			userToken: &models.UserToken{Token: validToken},
			verifyFn:  func(string) (string, error) { return "", nil },
			wantErr:   "user subject not found using the access token",
		},
		{
			name:      "a signature error does not start a refresh",
			userToken: &models.UserToken{Token: validToken},
			verifyFn:  func(string) (string, error) { return "", errSignature },
			wantErrIs: errSignature,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stubSeams(t)
			var refreshCount atomic.Int64
			loadUserToken = func(string) (*models.UserToken, error) { return tt.userToken, tt.getErr }
			idpRefreshExpiredToken = func(_ idp.TokenVerifier, gotToken string) (string, string, error) {
				refreshCount.Add(1)
				if tt.userToken != nil && gotToken != tt.userToken.Token {
					t.Errorf("want the stored token %q, got %q", tt.userToken.Token, gotToken)
				}
				if tt.refreshErr != nil {
					return "", "", tt.refreshErr
				}
				newToken := tt.refreshToken
				if newToken == "" {
					newToken = validToken
				}
				return tt.refreshSubject, newToken, nil
			}
			verifyFn := tt.verifyFn
			if verifyFn == nil {
				verifyFn = rejectExpired
			}

			err := CheckUserToken(&fakeVerifier{verifyFn: verifyFn}, testSubject)

			switch {
			case tt.wantErrIs != nil:
				if !errors.Is(err, tt.wantErrIs) {
					t.Errorf("want error %v, got %v", tt.wantErrIs, err)
				}
			case tt.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("want an error that contains %q, got %v", tt.wantErr, err)
				}
				if tt.refreshErr != nil && errors.Is(err, tt.refreshErr) {
					t.Error("the refresh failure reached the caller")
				}
			default:
				if err != nil {
					t.Errorf("want no error, got %v", err)
				}
			}
			if got := refreshCount.Load(); got != tt.wantRefresh {
				t.Errorf("want %v refresh calls, got %v", tt.wantRefresh, got)
			}
		})
	}
}

func TestPollingUserTokenEndsTheSession(t *testing.T) {
	s := stubSeams(t)
	loadUserToken = func(string) (*models.UserToken, error) {
		return &models.UserToken{Token: storedExpired}, nil
	}
	idpRefreshExpiredToken = func(idp.TokenVerifier, string) (string, string, error) {
		return "", "", errNoRefresh
	}

	_, cancelled := s.startPolling(rejectExpired)

	select {
	case cause := <-cancelled:
		if cause == nil || !strings.Contains(cause.Error(), "access token is expired") {
			t.Errorf("want an expired token cause, got %v", cause)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the poller did not end the session")
	}
}

// The regression net: the refresh engine writes the new token, so the session
// stays open and the poller refreshes one time for each expiry.
func TestPollingUserTokenKeepsTheSessionAfterARefresh(t *testing.T) {
	s := stubSeams(t)
	var mu sync.Mutex
	stored, refreshes, reads := storedExpired, 0, 0
	loadUserToken = func(string) (*models.UserToken, error) {
		mu.Lock()
		defer mu.Unlock()
		reads++
		return &models.UserToken{Token: stored}, nil
	}
	idpRefreshExpiredToken = func(_ idp.TokenVerifier, gotToken string) (string, string, error) {
		mu.Lock()
		defer mu.Unlock()
		if gotToken != storedExpired {
			t.Errorf("want the stored token %q, got %q", storedExpired, gotToken)
		}
		refreshes++
		stored = validToken
		return testSubject, validToken, nil
	}

	ctx, cancelled := s.startPolling(rejectExpired)

	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		done := reads >= 5
		mu.Unlock()
		if done {
			break
		}
		select {
		case cause := <-cancelled:
			t.Fatalf("the session ended after a good refresh: %v", cause)
		case <-deadline:
			t.Fatal("the poller did not run 5 rounds")
		case <-time.After(time.Millisecond):
		}
	}

	if ctx.Err() != nil {
		t.Errorf("want an open session, got %v", context.Cause(ctx))
	}
	mu.Lock()
	defer mu.Unlock()
	if refreshes != 1 {
		t.Errorf("want 1 refresh for 1 expiry, got %v", refreshes)
	}
}

// An immortal context would leak the poller, so the session ends instead. The
// panicking seam defaults prove the poll loop never ran.
func TestPollUserTokenRefusesAnImmortalContext(t *testing.T) {
	stubSeams(t)
	cancelled := make(chan error, 1)

	stopped := pollUserToken(context.Background(), func(cause error) {
		select {
		case cancelled <- cause:
		default:
		}
	}, &fakeVerifier{verifyFn: rejectExpired}, testSubject, time.Millisecond)

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("want the stop channel closed at once")
	}
	select {
	case cause := <-cancelled:
		if cause == nil || !strings.Contains(cause.Error(), "never cancels") {
			t.Errorf("want a never-cancels cause, got %v", cause)
		}
	default:
		t.Error("want the session ended, not a silent leak")
	}
}

// A valid token must not start a refresh or end the session. Every polling
// test asserts the goroutine stops, in the startPolling cleanup.
func TestPollingUserTokenKeepsAValidSessionOpen(t *testing.T) {
	s := stubSeams(t)
	var reads atomic.Int64
	loadUserToken = func(string) (*models.UserToken, error) {
		reads.Add(1)
		return &models.UserToken{Token: validToken}, nil
	}
	idpRefreshExpiredToken = func(idp.TokenVerifier, string) (string, string, error) {
		t.Error("a valid token must not start a refresh")
		return "", "", errNoRefresh
	}

	ctx, cancelled := s.startPolling(rejectExpired)

	deadline := time.After(5 * time.Second)
	for reads.Load() < 3 {
		select {
		case cause := <-cancelled:
			t.Fatalf("the session ended with a valid token: %v", cause)
		case <-deadline:
			t.Fatalf("want 3 polling rounds, got %v", reads.Load())
		case <-time.After(time.Millisecond):
		}
	}

	// The cleanup that startPolling registered cancels the context and waits
	// for the goroutine, so it fails the test if the poller ignores Done.
	if ctx.Err() != nil {
		t.Errorf("want an open session before the cleanup, got %v", context.Cause(ctx))
	}
}
