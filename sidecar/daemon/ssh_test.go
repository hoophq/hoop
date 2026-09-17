package daemon

import (
	"strings"
	"testing"
)

// SSH registers no codec and cannot have one, so the codec registry must not
// be asked to answer for it. Before the carve-out an ssh listener failed
// with "unsupported protocol", which told an operator the build lacks
// something rather than that their config is fine.
func TestSSHProtocolIsNotUnsupported(t *testing.T) {
	p := writeConfig(t, `{
      "listeners": [
        {"name":"jump","protocol":"ssh","listen":":2222","upstream":"unused:0"}
      ]
    }`)

	_, err := LoadConfig(p)
	if err != nil && strings.Contains(err.Error(), "unsupported protocol") {
		t.Fatalf("an ssh listener was refused for lacking a codec: %v", err)
	}
}
