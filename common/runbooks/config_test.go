package runbooks

import (
	"testing"
)

func TestConfigNormalizeGitURL(t *testing.T) {
	tests := []struct {
		input Config
		want  string
	}{
		{
			input: Config{GitURL: "https://git.kernel.org/pub/scm/bluetooth/bluez.git"},
			want:  "git.kernel.org/pub/scm/bluetooth/bluez",
		},
		{
			input: Config{GitURL: "https://github.com/torvalds/linux.git"},
			want:  "github.com/torvalds/linux",
		},
		{
			input: Config{GitURL: "git@github.com:user/repo.git"},
			want:  "github.com/user/repo",
		},
		{
			input: Config{GitURL: "ssh://git@gitlab.com/group/project.git"},
			want:  "gitlab.com/group/project",
		},
		{
			input: Config{GitURL: "git@gitlab.example.org:org/sub/project.git"},
			want:  "gitlab.example.org/org/sub/project",
		},
		{
			input: Config{GitURL: "ssh://user@bitbucket.org/team/repo.git"},
			want:  "bitbucket.org/team/repo",
		},
	}

	for _, tt := range tests {
		got := tt.input.GetNormalizedGitURL()

		if got != tt.want {
			t.Errorf("Config.GetNormalizedGitURL(%q) = %q; want %q", tt.input.GitURL, got, tt.want)
		}
	}
}
