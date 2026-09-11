package apisidecar

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
)

// A released sidecar must never be handed the configuration the row still
// holds from before the flip: the answer carries the instruction and the
// control plane's license, and nothing a lane could be built from. The
// license the row happens to store is ignored in favor of the plane's.
func TestDiskAnswerCarriesOnlyTheInstructionAndTheLicense(t *testing.T) {
	released := true
	stored := models.SidecarConfiguration(daemon.Config{
		LoadFromDisk: &released,
		License:      "stored-key",
		Listeners:    []daemon.ListenerConfig{{Name: "appdb"}},
		LogLevel:     "debug",
	})
	raw, err := json.Marshal(toDaemonConfig(stored, "L"))
	if err != nil {
		t.Fatalf("marshaling the disk answer: %v", err)
	}
	body := string(raw)
	for _, want := range []string{`"load_from_disk":true`, `"license":"L"`, `"listeners":null`} {
		if !strings.Contains(body, want) {
			t.Errorf("the answer does not carry %s: %s", want, body)
		}
	}
	for _, leaked := range []string{"appdb", "debug", "stored-key"} {
		if strings.Contains(body, leaked) {
			t.Errorf("the stored configuration leaked %q into the answer: %s", leaked, body)
		}
	}
}
