package apisidecar

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
)

// The disk-mode answer carries exactly the instruction and the license: no
// listener list, no audit or admin block, nothing a lane could be built from.
// Any extra key serialized here would read as a configuration rather than an
// instruction, and contradict the documented response shape.
func TestDiskAnswerCarriesOnlyTheInstructionAndTheLicense(t *testing.T) {
	for _, tc := range []struct {
		name    string
		license string
		want    []string
	}{
		{"licensed", "L", []string{"license", "load_from_disk"}},
		{"free tier", "", []string{"load_from_disk"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(diskModeConfig{LoadFromDisk: true, License: tc.license})
			if err != nil {
				t.Fatalf("marshaling the disk answer: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("decoding the disk answer: %v", err)
			}
			keys := slices.Sorted(maps.Keys(got))
			if !slices.Equal(keys, tc.want) {
				t.Errorf("answer keys = %v, want exactly %v: %s", keys, tc.want, raw)
			}
			if got["load_from_disk"] != true {
				t.Errorf("load_from_disk = %v, want true", got["load_from_disk"])
			}
		})
	}
}

// A control-plane-owned sidecar (load_from_disk absent or false) must never be
// served the key: an older sidecar decodes with DisallowUnknownFields, would
// reject the whole config, and could never recover once the flag is off.
func TestControlPlaneAnswerNeverCarriesLoadFromDisk(t *testing.T) {
	off := false
	stored := models.SidecarConfiguration(daemon.Config{
		LoadFromDisk: &off,
		Listeners:    []daemon.ListenerConfig{{Name: "appdb"}},
	})
	raw, err := json.Marshal(toDaemonConfig(stored, "L"))
	if err != nil {
		t.Fatalf("marshaling the answer: %v", err)
	}
	if strings.Contains(string(raw), "load_from_disk") {
		t.Errorf("the control-plane answer carries load_from_disk: %s", raw)
	}
}
