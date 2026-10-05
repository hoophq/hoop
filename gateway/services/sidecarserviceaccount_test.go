package services

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/gateway/externaljwt"
	"github.com/hoophq/hoop/gateway/models"
)

const (
	k8sIssuer    = "https://container.googleapis.com/v1/projects/p/locations/eu/clusters/eu"
	googleIssuer = "https://accounts.google.com"
	testJWKS     = `{"keys":[{"kty":"RSA","kid":"k1","n":"0vx7agoebGcQSuuPiLJXZptN9nndrQmbXEps2aiAFbWhM78LhWx4cbbfAAtVT86zwu1RK7aPFFxuhDR1L6tSoc_BJECPebWKRXjBZCiFV4n3oknjhMstn64tZ_2W-5JsGY4Hc5n9yBXArwl93lqt7_RN5w6Cf0h4QyQ5v-65YGjQR0_FDW2QvzqY368QQMicAtaSqzs8KJZgnYb9c7d0zgdAZHzu6qMQvRL5hajrn1n91CbOpbISD08qNLyrdkt-bFTWhAI4vMQFh6WeZu0fM4lFd2NcRwr3XPksINHaQ-G_xBniIqbw0Ls1jF44-csFCur-kEgU8awapJzKnqDKgw","e":"AQAB"}]}`
)

var baseTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func mapping(id, org, claim, pattern, template string, age time.Duration) models.SidecarServiceAccount {
	return models.SidecarServiceAccount{ID: id, OrgID: org, Name: "m-" + id, Issuer: k8sIssuer, Audience: "https://hoop.example.com",
		Claim: claim, SubjectPattern: pattern, NameTemplate: template, CreatedAt: baseTime.Add(-age)}
}

func TestMatchSubjectPattern(t *testing.T) {
	for _, tt := range []struct {
		pattern, value, capture string
		ok                      bool
	}{
		{"system:serviceaccount:ws-1:hoop-sidecar", "system:serviceaccount:ws-1:hoop-sidecar", "", true},
		{"system:serviceaccount:ws-1:hoop-sidecar", "system:serviceaccount:ws-2:hoop-sidecar", "", false},
		{"system:serviceaccount:*:hoop-sidecar", "system:serviceaccount:ws-1:hoop-sidecar", "ws-1", true},
		// '*' stands for one or more characters, never none.
		{"system:serviceaccount:*:hoop-sidecar", "system:serviceaccount::hoop-sidecar", "", false},
		{"system:serviceaccount:*:hoop-sidecar", "system:serviceaccount:ws-1:other", "", false},
		{"*@p.iam.gserviceaccount.com", "sc-1@p.iam.gserviceaccount.com", "sc-1", true},
		{"sidecar-*", "sidecar-", "", false},
		// Prefix and suffix must not overlap in the value.
		{"ab*ba", "aba", "", false},
		{"*", "anything", "anything", true},
	} {
		capture, ok := matchSubjectPattern(tt.pattern, tt.value)
		if ok != tt.ok || capture != tt.capture {
			t.Errorf("match(%q, %q) = %q, %v; want %q, %v", tt.pattern, tt.value, capture, ok, tt.capture, tt.ok)
		}
	}
}

func TestMatchSidecarServiceAccount(t *testing.T) {
	const sub = "system:serviceaccount:ws-1:hoop-sidecar"
	k8s := externaljwt.OIDCClaims{Issuer: k8sIssuer, Subject: sub}
	googleClaims := externaljwt.OIDCClaims{Issuer: googleIssuer, Subject: "1234", Email: "sc-1@p.iam.gserviceaccount.com", EmailVerified: true}

	google := func(id, pattern string, allowAny bool) models.SidecarServiceAccount {
		m := mapping(id, "org-a", SidecarClaimEmail, pattern, "gcp-{1}", 0)
		m.Issuer = googleIssuer
		m.AllowAnySubject = allowAny
		return m
	}
	anyK8s := mapping("any", "org-a", SidecarClaimSub, "*", "sc-{1}", 0)
	anyK8sAllowed := anyK8s
	anyK8sAllowed.AllowAnySubject = true

	for _, tt := range []struct {
		name     string
		mappings []models.SidecarServiceAccount
		claims   externaljwt.OIDCClaims
		wantID   string
		wantName string
		wantErr  error
	}{
		{
			name: "exact beats a longer wildcard",
			mappings: []models.SidecarServiceAccount{
				mapping("wild", "org-a", SidecarClaimSub, "system:serviceaccount:ws-1:*", "wild-{1}", time.Hour),
				mapping("exact", "org-a", SidecarClaimSub, sub, "legacy-payments", 0),
			},
			claims: k8s, wantID: "exact", wantName: "legacy-payments",
		},
		{
			name: "longest literal wins among wildcards",
			mappings: []models.SidecarServiceAccount{
				mapping("short", "org-a", SidecarClaimSub, "system:serviceaccount:*", "short-{1}", time.Hour),
				mapping("long", "org-a", SidecarClaimSub, "system:serviceaccount:*:hoop-sidecar", "gke-eu-{1}", 0),
			},
			claims: k8s, wantID: "long", wantName: "gke-eu-ws-1",
		},
		{
			name: "equal literal length goes to the oldest",
			mappings: []models.SidecarServiceAccount{
				mapping("young", "org-a", SidecarClaimSub, "system:serviceaccount:ws-*:hoop-sidecar", "young-{1}", 0),
				mapping("old", "org-a", SidecarClaimSub, "system:serviceaccount:*s-1:hoop-sidecar", "old-{1}", time.Hour),
			},
			claims: k8s, wantID: "old", wantName: "old-w",
		},
		{
			name: "a match in two organizations is refused",
			mappings: []models.SidecarServiceAccount{
				mapping("a", "org-a", SidecarClaimSub, "system:serviceaccount:*:hoop-sidecar", "a-{1}", 0),
				mapping("b", "org-b", SidecarClaimSub, sub, "b-sidecar", 0),
			},
			claims: k8s, wantErr: ErrSidecarIdentityAmbiguous,
		},
		{
			name:     "two organizations, one matching, is not ambiguous",
			mappings: []models.SidecarServiceAccount{mapping("a", "org-a", SidecarClaimSub, "system:serviceaccount:*:hoop-sidecar", "a-{1}", 0), mapping("b", "org-b", SidecarClaimSub, "system:serviceaccount:*:other", "b-{1}", 0)},
			claims:   k8s, wantID: "a", wantName: "a-ws-1",
		},
		{
			name:     "no pattern matches",
			mappings: []models.SidecarServiceAccount{mapping("a", "org-a", SidecarClaimSub, "system:serviceaccount:*:other", "a-{1}", 0)},
			claims:   k8s, wantErr: ErrSidecarIdentityNotAllowed,
		},
		{
			name:     "email needs email_verified",
			mappings: []models.SidecarServiceAccount{google("g", "*@p.iam.gserviceaccount.com", false)},
			claims:   externaljwt.OIDCClaims{Issuer: googleIssuer, Email: "sc-1@p.iam.gserviceaccount.com", EmailVerified: false},
			wantErr:  ErrSidecarIdentityNotAllowed,
		},
		{
			name:     "verified email matches",
			mappings: []models.SidecarServiceAccount{google("g", "*@p.iam.gserviceaccount.com", false)},
			claims:   googleClaims, wantID: "g", wantName: "gcp-sc-1",
		},
		{
			name: "google is fenced to one project even with allow_any_subject",
			mappings: []models.SidecarServiceAccount{
				google("bare", "*", true),
				google("other-project", "*@q.iam.gserviceaccount.com", false),
				google("star-in-domain", "sc-1@*.iam.gserviceaccount.com", false),
			},
			claims: googleClaims, wantErr: ErrSidecarIdentityNotAllowed,
		},
		{
			name:     "bare * needs allow_any_subject",
			mappings: []models.SidecarServiceAccount{anyK8s},
			claims:   externaljwt.OIDCClaims{Issuer: k8sIssuer, Subject: "ws-1"},
			wantErr:  ErrSidecarIdentityNotAllowed,
		},
		{
			name:     "bare * with allow_any_subject captures the whole subject",
			mappings: []models.SidecarServiceAccount{anyK8sAllowed},
			claims:   externaljwt.OIDCClaims{Issuer: k8sIssuer, Subject: "ws-1"},
			wantID:   "any", wantName: "sc-ws-1",
		},
		{
			name:     "a rendered name that is not a resource name is refused",
			mappings: []models.SidecarServiceAccount{mapping("a", "org-a", SidecarClaimSub, "system:serviceaccount:*", "sc-{1}", 0)},
			claims:   k8s, wantErr: ErrSidecarIdentityInvalidName,
		},
		{
			name:     "a reserved rendered name is refused",
			mappings: []models.SidecarServiceAccount{mapping("a", "org-a", SidecarClaimSub, "system:serviceaccount:*:hoop-sidecar", "handshake", 0)},
			claims:   k8s, wantErr: ErrSidecarIdentityInvalidName,
		},
		{
			name:     "two stars never match",
			mappings: []models.SidecarServiceAccount{mapping("a", "org-a", SidecarClaimSub, "system:*:*:hoop-sidecar", "a-x", 0)},
			claims:   k8s, wantErr: ErrSidecarIdentityNotAllowed,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := MatchSidecarServiceAccount(tt.mappings, tt.claims)
			if tt.wantErr != nil {
				var refusal *SidecarIdentityRefusal
				if !errors.Is(err, tt.wantErr) || !errors.As(err, &refusal) {
					t.Fatalf("want a refusal wrapping %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ServiceAccount.ID != tt.wantID || got.Name != tt.wantName {
				t.Errorf("got mapping %q name %q, want %q %q", got.ServiceAccount.ID, got.Name, tt.wantID, tt.wantName)
			}
		})
	}
}

func TestMatchSidecarServiceAccountNamesTheUnverifiedEmail(t *testing.T) {
	m := mapping("g", "org-a", SidecarClaimEmail, "*@p.iam.gserviceaccount.com", "gcp-{1}", 0)
	m.Issuer = googleIssuer
	_, err := MatchSidecarServiceAccount([]models.SidecarServiceAccount{m},
		externaljwt.OIDCClaims{Email: "sc-1@p.iam.gserviceaccount.com"})
	if err == nil || !strings.Contains(err.Error(), "email_verified") {
		t.Fatalf("the refusal must name email_verified, got %v", err)
	}
}

func TestValidateSidecarServiceAccount(t *testing.T) {
	valid := func() *models.SidecarServiceAccount {
		return &models.SidecarServiceAccount{Name: "gke-eu", Issuer: k8sIssuer, Audience: "https://hoop.example.com",
			Claim: SidecarClaimSub, SubjectPattern: "system:serviceaccount:*:hoop-sidecar", NameTemplate: "gke-eu-{1}"}
	}
	validGoogle := func() *models.SidecarServiceAccount {
		sa := valid()
		sa.Issuer, sa.Claim, sa.SubjectPattern, sa.NameTemplate = googleIssuer, SidecarClaimEmail, "sidecar-*@my-project.iam.gserviceaccount.com", "sidecar-{1}"
		return sa
	}
	for _, tt := range []struct {
		name   string
		edit   func(*models.SidecarServiceAccount)
		google bool
		field  string // empty: valid
	}{
		{name: "kubernetes mapping"},
		{name: "google mapping", google: true},
		{name: "exact pattern and fixed name", edit: func(s *models.SidecarServiceAccount) {
			s.SubjectPattern, s.NameTemplate = "system:serviceaccount:ws-1:hoop-sidecar", "payments"
		}},
		{name: "bare * with allow_any_subject", edit: func(s *models.SidecarServiceAccount) {
			s.SubjectPattern, s.AllowAnySubject = "*", true
		}},
		{name: "static jwks with an issuer that is not a URL", edit: func(s *models.SidecarServiceAccount) {
			s.Issuer, s.JWKS = "kubernetes/serviceaccount", []byte(testJWKS)
		}},
		{name: "invalid label", edit: func(s *models.SidecarServiceAccount) { s.Name = "a b" }, field: "name"},
		{name: "unknown claim", edit: func(s *models.SidecarServiceAccount) { s.Claim = "aud" }, field: "claim"},
		{name: "no audience", edit: func(s *models.SidecarServiceAccount) { s.Audience = "" }, field: "audience"},
		{name: "two stars", edit: func(s *models.SidecarServiceAccount) { s.SubjectPattern = "system:*:*:x" }, field: "subject_pattern"},
		{name: "bare * without allow_any_subject", edit: func(s *models.SidecarServiceAccount) { s.SubjectPattern = "*" }, field: "subject_pattern"},
		{name: "http issuer without jwks", edit: func(s *models.SidecarServiceAccount) { s.Issuer = "http://issuer.example.com" }, field: "issuer"},
		{name: "issuer with a query", edit: func(s *models.SidecarServiceAccount) { s.Issuer = "https://issuer.example.com?x=1" }, field: "issuer"},
		{name: "jwks with no key", edit: func(s *models.SidecarServiceAccount) { s.JWKS = []byte(`{"keys":[]}`) }, field: "jwks"},
		{name: "{1} with an exact pattern", edit: func(s *models.SidecarServiceAccount) {
			s.SubjectPattern = "system:serviceaccount:ws-1:hoop-sidecar"
		}, field: "name_template"},
		{name: "template rendering an invalid name", edit: func(s *models.SidecarServiceAccount) { s.NameTemplate = "gke/{1}" }, field: "name_template"},
		{name: "template rendering a reserved name", edit: func(s *models.SidecarServiceAccount) {
			s.SubjectPattern, s.NameTemplate = "system:serviceaccount:ws-1:hoop-sidecar", "configuration"
		}, field: "name_template"},
		{name: "google with claim sub", google: true, edit: func(s *models.SidecarServiceAccount) { s.Claim = SidecarClaimSub }, field: "claim"},
		{name: "google bare * even with allow_any_subject", google: true, edit: func(s *models.SidecarServiceAccount) {
			s.SubjectPattern, s.AllowAnySubject = "*", true
		}, field: "subject_pattern"},
		{name: "google with no project domain", google: true, edit: func(s *models.SidecarServiceAccount) {
			s.SubjectPattern = "sidecar-*@gmail.com"
		}, field: "subject_pattern"},
		{name: "google with * after the @", google: true, edit: func(s *models.SidecarServiceAccount) {
			s.SubjectPattern = "sidecar-1@*.iam.gserviceaccount.com"
		}, field: "subject_pattern"},
		{name: "google without the scheme is fenced too", google: true, edit: func(s *models.SidecarServiceAccount) {
			s.Issuer, s.JWKS, s.SubjectPattern = "accounts.google.com", []byte(testJWKS), "*@gmail.com"
		}, field: "subject_pattern"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sa := valid()
			if tt.google {
				sa = validGoogle()
			}
			if tt.edit != nil {
				tt.edit(sa)
			}
			err := ValidateSidecarServiceAccount(sa)
			if tt.field == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidSidecarServiceAccount) ||
				!strings.HasPrefix(err.Error(), ErrInvalidSidecarServiceAccount.Error()+": "+tt.field+":") {
				t.Fatalf("want a %s refusal, got %v", tt.field, err)
			}
		})
	}
}
