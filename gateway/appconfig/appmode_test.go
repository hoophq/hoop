package appconfig

import "testing"

func TestResolveAppMode(t *testing.T) {
	for _, tt := range []struct {
		name    string
		mode    AppMode
		want    AppMode
		wantErr bool
	}{
		{name: "zero value", mode: "", want: AppModeGateway},
		{name: "gateway", mode: AppModeGateway, want: AppModeGateway},
		{name: "control-plane", mode: AppModeControlPlane, want: AppModeControlPlane},
		// The mode is a typed constant chosen by a subcommand, so anything
		// else is a programming error and must stop startup.
		{name: "unknown", mode: AppMode("agent"), wantErr: true},
		{name: "wrong spelling", mode: AppMode("controlplane"), wantErr: true},
		{name: "wrong case", mode: AppMode("Control-Plane"), wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveAppMode(tt.mode)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("mode %q: want error, got mode %q", tt.mode, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("mode %q: unexpected error: %v", tt.mode, err)
			}
			if got != tt.want {
				t.Errorf("mode %q: got %q, want %q", tt.mode, got, tt.want)
			}
		})
	}
}

// An unloaded config must read as the gateway, so nothing that runs before
// Load — or in a test that never calls it — sees an empty mode.
func TestAppModeZeroValue(t *testing.T) {
	var c Config
	if got := c.AppMode(); got != AppModeGateway {
		t.Errorf("got %q, want %q", got, AppModeGateway)
	}
	if c.IsControlPlane() {
		t.Error("zero config reports control plane")
	}
}
