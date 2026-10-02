package services

import (
	"reflect"
	"strings"
	"testing"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/sidecar/daemon"
)

func sidecarWith(name string, listeners ...daemon.ListenerConfig) *models.Sidecar {
	return &models.Sidecar{
		ID:            "9b2a8b4e-5c0e-4a51-9b7a-1f3e2d4c5b6a",
		OrgID:         "org-1",
		Name:          name,
		Configuration: models.SidecarConfiguration{Listeners: listeners},
	}
}

func TestProjectListenersMapsEveryProtocol(t *testing.T) {
	cases := []struct {
		protocol, typ, subtype string
	}{
		{"postgres", "database", "postgres"},
		{"mysql", "database", "mysql"},
		{"mssql", "database", "mssql"},
		{"mongodb", "database", "mongodb"},
		{"ssh", "application", "ssh"},
		{"http", "httpproxy", "httpproxy"},
		{"clickhouse", "custom", "clickhouse"},
		{"grpc", "custom", "grpc"},
		{"spanner", "custom", "spanner"},
	}
	for _, tc := range cases {
		t.Run(tc.protocol, func(t *testing.T) {
			sc := sidecarWith("pay", daemon.ListenerConfig{Name: "lane", Protocol: tc.protocol})
			got, err := ProjectListeners("org-1", sc)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("want 1 connection, got %d", len(got))
			}
			if got[0].Type != tc.typ || got[0].SubType.String != tc.subtype || !got[0].SubType.Valid {
				t.Errorf("want %s/%s, got %s/%s", tc.typ, tc.subtype, got[0].Type, got[0].SubType.String)
			}
		})
	}
	// The table and the daemon's own list must agree, or a protocol a
	// listener may declare has no mirror.
	for _, p := range daemon.Protocols() {
		if _, err := ProjectListeners("org-1", sidecarWith("pay", daemon.ListenerConfig{Name: "lane", Protocol: p})); err != nil {
			t.Errorf("protocol %q is one a listener may declare but has no mapping: %v", p, err)
		}
	}
}

func TestProjectListenersRendersTheMirror(t *testing.T) {
	sc := sidecarWith("payments",
		daemon.ListenerConfig{Name: "appdb", Protocol: "postgres"},
		daemon.ListenerConfig{Name: "api.v2_beta", Protocol: "http"},
	)
	got, err := ProjectListeners("org-1", sc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want one connection per listener, got %d", len(got))
	}
	// Config order, name composition.
	if got[0].Name != "payments-appdb" || got[1].Name != "payments-api.v2_beta" {
		t.Errorf("want names in config order, got %q, %q", got[0].Name, got[1].Name)
	}
	for _, c := range got {
		if c.ID != "" {
			t.Errorf("%s: the ID is the writer's to resolve, got %q", c.Name, c.ID)
		}
		if c.OrgID != "org-1" {
			t.Errorf("%s: org_id=%q", c.Name, c.OrgID)
		}
		if c.ManagedBy.String != models.ConnectionManagedBySidecar || !c.ManagedBy.Valid {
			t.Errorf("%s: managed_by=%+v", c.Name, c.ManagedBy)
		}
		if !c.IsSidecarBacked() || c.SidecarID.String != sc.ID {
			t.Errorf("%s: sidecar_id=%+v", c.Name, c.SidecarID)
		}
		if c.AgentID.Valid {
			t.Errorf("%s: a mirror has no agent, got %+v", c.Name, c.AgentID)
		}
		if c.AccessModeConnect != "enabled" || c.AccessModeExec != "disabled" ||
			c.AccessModeRunbooks != "disabled" || c.AccessSchema != "disabled" {
			t.Errorf("%s: want connect only, got connect=%s exec=%s runbooks=%s schema=%s",
				c.Name, c.AccessModeConnect, c.AccessModeExec, c.AccessModeRunbooks, c.AccessSchema)
		}
	}
	if got[0].SidecarListener.String != "appdb" || got[1].SidecarListener.String != "api.v2_beta" {
		t.Errorf("want the listener name kept verbatim, got %q, %q",
			got[0].SidecarListener.String, got[1].SidecarListener.String)
	}
}

func TestProjectListenersRefusesWhatItCannotMirror(t *testing.T) {
	cases := []struct {
		name     string
		sidecar  string
		listener daemon.ListenerConfig
		wantErr  string
	}{
		{"unknown protocol", "pay", daemon.ListenerConfig{Name: "appdb", Protocol: "oracle"}, `no connection type for protocol "oracle"`},
		{"empty protocol", "pay", daemon.ListenerConfig{Name: "appdb", Protocol: ""}, `no connection type for protocol ""`},
		{"listener name with a space", "pay", daemon.ListenerConfig{Name: "app db", Protocol: "postgres"}, "connection name:"},
		{"empty listener name", "pay", daemon.ListenerConfig{Name: "", Protocol: "postgres"}, "connection name:"},
		{"sidecar name with a slash", "pay/ments", daemon.ListenerConfig{Name: "appdb", Protocol: "postgres"}, "connection name:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ProjectListeners("org-1", sidecarWith(tc.sidecar, tc.listener))
			if err == nil {
				t.Fatalf("want an error, got %+v", got)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("want error containing %q, got %q", tc.wantErr, err)
			}
			if got != nil {
				t.Errorf("want no partial result, got %d connections", len(got))
			}
		})
	}

	// One bad listener fails the whole projection: a sidecar is mirrored
	// whole or not at all.
	_, err := ProjectListeners("org-1", sidecarWith("pay",
		daemon.ListenerConfig{Name: "ok", Protocol: "postgres"},
		daemon.ListenerConfig{Name: "bad", Protocol: "redis"},
	))
	if err == nil {
		t.Error("want the projection to fail on the second listener")
	}
}

func TestProjectListenersWithoutListeners(t *testing.T) {
	for _, sc := range []*models.Sidecar{
		sidecarWith("pay"),
		{ID: "id", Name: "pay"},
	} {
		got, err := ProjectListeners("org-1", sc)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("want no connections, got %d", len(got))
		}
	}
}

func TestProjectListenersIsDeterministic(t *testing.T) {
	sc := sidecarWith("pay",
		daemon.ListenerConfig{Name: "b", Protocol: "mysql"},
		daemon.ListenerConfig{Name: "a", Protocol: "postgres"},
		daemon.ListenerConfig{Name: "c", Protocol: "ssh"},
	)
	first, err := ProjectListeners("org-1", sc)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ProjectListeners("org-1", sc)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("same input, different output:\n%+v\n%+v", first, second)
	}
	for i, want := range []string{"b", "a", "c"} {
		if first[i].SidecarListener.String != want {
			t.Errorf("position %d: want listener %q (config order, not sorted), got %q", i, want, first[i].SidecarListener.String)
		}
	}
}
