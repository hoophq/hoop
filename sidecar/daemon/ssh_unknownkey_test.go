package daemon

import "testing"

// SSHConfig unmarshals itself, and a type that does that does NOT inherit the
// outer decoder's DisallowUnknownFields. Without re-imposing it a typo inside
// the ssh block would load silently — on the block whose keys decide what a
// listener admits and which account it becomes.
func TestSSHBlockRefusesUnknownKeys(t *testing.T) {
	p := sshConfig(t, map[string]any{"capabilities_allowedd": []string{"exec"}})
	mustContain(t, loadErr(t, p), "capabilities_allowedd")
}
