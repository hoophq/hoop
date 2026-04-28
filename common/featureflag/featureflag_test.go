package featureflag

import "testing"

func TestLookup(t *testing.T) {
	f, ok := Lookup("experimental.nightly_flag")
	if !ok {
		t.Fatal("expected experimental.nightly_flag to be in catalog")
	}
	if f.Stability != StabilityExperimental {
		t.Fatalf("expected stability experimental, got %s", f.Stability)
	}

	_, ok = Lookup("nonexistent.flag")
	if ok {
		t.Fatal("expected nonexistent flag to not be in catalog")
	}
}

func TestAll(t *testing.T) {
	flags := All()
	if len(flags) == 0 {
		t.Fatal("expected at least one flag in catalog")
	}
	for i := 1; i < len(flags); i++ {
		if flags[i-1].Name >= flags[i].Name {
			t.Fatalf("flags not sorted: %s >= %s", flags[i-1].Name, flags[i].Name)
		}
	}
}

func TestIsEnabled_DefaultFalse(t *testing.T) {
	if IsEnabled("org-1", "experimental.nightly_flag") {
		t.Fatal("expected default to be false")
	}
}

func TestSetAndIsEnabled(t *testing.T) {
	Set("org-test", "experimental.nightly_flag", true)
	if !IsEnabled("org-test", "experimental.nightly_flag") {
		t.Fatal("expected flag to be enabled after Set")
	}
	Set("org-test", "experimental.nightly_flag", false)
	if IsEnabled("org-test", "experimental.nightly_flag") {
		t.Fatal("expected flag to be disabled after Set(false)")
	}
}

func TestIsEnabled_UnknownFlag(t *testing.T) {
	if IsEnabled("org-1", "does.not.exist") {
		t.Fatal("unknown flag should return false")
	}
}

func TestSnapshotForOrg(t *testing.T) {
	Set("org-snap", "experimental.nightly_flag", true)
	snap := SnapshotForOrg("org-snap")
	if !snap["experimental.nightly_flag"] {
		t.Fatal("snapshot should reflect Set value")
	}

	snap2 := SnapshotForOrg("org-empty")
	for name, val := range snap2 {
		f, _ := Lookup(name)
		if val != f.Default {
			t.Fatalf("snapshot for unknown org should use defaults, flag=%s", name)
		}
	}
}

func TestSetAll(t *testing.T) {
	SetAll("org-all", map[string]bool{
		"experimental.nightly_flag": true,
		"garbage.unknown":           true,
	})
	if !IsEnabled("org-all", "experimental.nightly_flag") {
		t.Fatal("expected nightly_flag enabled via SetAll")
	}
	snap := SnapshotForOrg("org-all")
	if _, found := snap["garbage.unknown"]; found {
		t.Fatal("SetAll should filter out unknown flags")
	}
}
