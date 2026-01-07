package apirunbooks

import (
	"testing"
)

func TestValidateGitURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		// Valid SSH URLs
		{
			name:    "valid SSH URL with .git",
			url:     "git@github.com:hoophq/hoop.git",
			wantErr: false,
		},
		{
			name:    "valid SSH URL without .git",
			url:     "git@github.com:hoophq/hoop",
			wantErr: false,
		},
		{
			name:    "valid SSH URL with nested path",
			url:     "git@gitlab.com:group/subgroup/repo.git",
			wantErr: false,
		},
		{
			name:    "valid SSH URL with hyphen in username",
			url:     "git-user@github.com:user/repo.git",
			wantErr: false,
		},
		{
			name:    "valid SSH URL with dots in hostname",
			url:     "git@git.example.com:user/repo.git",
			wantErr: false,
		},

		// Valid HTTPS URLs
		{
			name:    "valid HTTPS URL",
			url:     "https://github.com/hoophq/runbooks",
			wantErr: false,
		},
		{
			name:    "valid HTTPS URL with .git",
			url:     "https://github.com/hoophq/runbooks.git",
			wantErr: false,
		},
		{
			name:    "valid HTTPS URL with nested path",
			url:     "https://gitlab.com/group/subgroup/repo.git",
			wantErr: false,
		},

		// Valid HTTP URLs
		{
			name:    "valid HTTP URL",
			url:     "http://github.com/hoophq/runbooks",
			wantErr: false,
		},

		// Valid git:// URLs
		{
			name:    "valid git protocol URL",
			url:     "git://github.com/user/repo",
			wantErr: false,
		},
		{
			name:    "valid git protocol URL with .git",
			url:     "git://github.com/user/repo.git",
			wantErr: false,
		},

		// Valid ssh:// URLs
		{
			name:    "valid ssh:// URL",
			url:     "ssh://git@github.com/user/repo.git",
			wantErr: false,
		},

		// Invalid URLs
		{
			name:    "empty URL",
			url:     "",
			wantErr: true,
		},
		{
			name:    "invalid scheme - ftp",
			url:     "ftp://github.com/user/repo",
			wantErr: true,
		},
		{
			name:    "invalid scheme - file",
			url:     "file:///path/to/repo",
			wantErr: true,
		},
		{
			name:    "no scheme",
			url:     "github.com/user/repo",
			wantErr: true,
		},
		{
			name:    "malformed URL",
			url:     "not-a-url",
			wantErr: true,
		},
		{
			name:    "SSH format missing repo path",
			url:     "git@github.com:",
			wantErr: true,
		},
		{
			name:    "SSH format missing host",
			url:     "git@:user/repo.git",
			wantErr: true,
		},
		{
			name:    "URL without host",
			url:     "https:///user/repo",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateGitURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateGitURL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
