package federation

import (
	"strings"
	"testing"
)

func TestResolveIdentity_HappyPaths(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		template string
		ctx      IdentityContext
		want     string
	}{
		{
			name:     "default mapping passes user email through unchanged",
			src:      "",
			template: "",
			ctx:      IdentityContext{UserEmail: "alice@acme.com"},
			want:     "alice@acme.com",
		},
		{
			name:     "explicit defaults match implicit defaults",
			src:      "$.user.email",
			template: "{user.email}",
			ctx:      IdentityContext{UserEmail: "bob@acme.com"},
			want:     "bob@acme.com",
		},
		{
			name:     "literal prefix in template renders verbatim",
			src:      "$.user.email",
			template: "data-team-{user.email}",
			ctx:      IdentityContext{UserEmail: "carol@acme.com"},
			want:     "data-team-carol@acme.com",
		},
		{
			name:     "user.id source feeds id-only templates",
			src:      "$.user.id",
			template: "sa-{user.id}@proj.iam.gserviceaccount.com",
			ctx:      IdentityContext{UserID: "abc-123", UserEmail: "ignored@e.com"},
			want:     "sa-abc-123@proj.iam.gserviceaccount.com",
		},
		{
			name:     "user.email_local extracts the local part for SA email composition",
			src:      "$.user.email",
			template: "{user.email_local}@proj.iam.gserviceaccount.com",
			ctx:      IdentityContext{UserEmail: "matheusmachadoufsc@gmail.com"},
			want:     "matheusmachadoufsc@proj.iam.gserviceaccount.com",
		},
		{
			name:     "user.email_local preserves dots and plus signs verbatim (sanitization is caller's job)",
			src:      "$.user.email",
			template: "{user.email_local}@proj.iam.gserviceaccount.com",
			ctx:      IdentityContext{UserEmail: "first.last+work@example.com"},
			want:     "first.last+work@proj.iam.gserviceaccount.com",
		},
		{
			name:     "user.email_local splits on the LAST @ so emails with quoted @ collapse safely",
			src:      "$.user.email",
			template: "{user.email_local}@proj.iam.gserviceaccount.com",
			ctx:      IdentityContext{UserEmail: "weird@subdomain@example.com"},
			want:     "weird@subdomain@proj.iam.gserviceaccount.com",
		},
		{
			name:     "user.email_local with no @ passes the value through unchanged",
			src:      "$.user.email",
			template: "{user.email_local}@proj.iam.gserviceaccount.com",
			ctx:      IdentityContext{UserEmail: "already-a-handle"},
			want:     "already-a-handle@proj.iam.gserviceaccount.com",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveIdentity(tc.src, tc.template, tc.ctx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveIdentity_ErrorPaths(t *testing.T) {
	cases := []struct {
		name        string
		src         string
		template    string
		ctx         IdentityContext
		wantErrSubs string
	}{
		{
			name:        "unsupported source attribute is rejected",
			src:         "$.user.groups",
			template:    "{user.email}",
			ctx:         IdentityContext{UserEmail: "x@y.com"},
			wantErrSubs: "unsupported identity source attribute",
		},
		{
			name:        "empty source value fails resolution",
			src:         "$.user.email",
			template:    "{user.email}",
			ctx:         IdentityContext{UserEmail: ""},
			wantErrSubs: "resolved to empty value",
		},
		{
			name:        "unknown placeholder in template is rejected loudly",
			src:         "$.user.email",
			template:    "{user.foo}",
			ctx:         IdentityContext{UserEmail: "x@y.com"},
			wantErrSubs: "unknown placeholder",
		},
		{
			name:        "unknown placeholder error advertises user.email_local as supported",
			src:         "$.user.email",
			template:    "{user.handle}@proj.iam.gserviceaccount.com",
			ctx:         IdentityContext{UserEmail: "x@y.com"},
			wantErrSubs: "{user.email_local}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ResolveIdentity(tc.src, tc.template, tc.ctx)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrSubs)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubs) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSubs)
			}
		})
	}
}
