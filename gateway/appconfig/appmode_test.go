package appconfig

import "testing"

func TestParseAppMode(t *testing.T) {
	for _, tt := range []struct {
		env     string
		want    AppMode
		wantErr bool
	}{
		{env: "", want: AppModeGateway},
		{env: "gateway", want: AppModeGateway},
		{env: "control-plane", want: AppModeControlPlane},
		{env: "CONTROL-PLANE", want: AppModeControlPlane},
		{env: " control-plane ", want: AppModeControlPlane},
		{env: "control_plane", wantErr: true},
		{env: "controlplane", wantErr: true},
		{env: "agent", wantErr: true},
	} {
		t.Run(tt.env, func(t *testing.T) {
			got, err := parseAppMode(tt.env)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("APP_MODE=%q: want error, got mode %q", tt.env, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("APP_MODE=%q: unexpected error: %v", tt.env, err)
			}
			if got != tt.want {
				t.Errorf("APP_MODE=%q: got %q, want %q", tt.env, got, tt.want)
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
