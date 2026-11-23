package featureflag

import (
	"slices"
	"sync"
	"testing"
)

// testFlag is an in-catalog fixture used only by these tests to exercise the
// flag system (Lookup/Set/SetAll/IsEnabled/SnapshotForOrg) independently of any
// real flag's lifecycle. _test.go files are never compiled into the production
// binary, so this fixture never ships.
const testFlag = "experimental.test_fixture"

func init() {
	catalog[testFlag] = Flag{
		Name:        testFlag,
		Description: "Test-only fixture flag (never registered in production builds)",
		Default:     false,
		Stability:   StabilityExperimental,
		Components:  []Component{ComponentGateway},
	}
}

func TestLookup(t *testing.T) {
	f, ok := Lookup(testFlag)
	if !ok {
		t.Fatalf("expected %s to be in catalog", testFlag)
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

func TestIsEnabled_FallsBackToDefault(t *testing.T) {
	// With no per-org override, IsEnabled must return the flag's catalog
	// default. Asserted against the actual Default so the test stays correct
	// regardless of the default's value.
	f, ok := Lookup(testFlag)
	if !ok {
		t.Fatalf("expected %s to be in catalog", testFlag)
	}
	if got := IsEnabled("org-no-override", testFlag); got != f.Default {
		t.Fatalf("expected IsEnabled to fall back to default %v, got %v", f.Default, got)
	}
}

func TestSetAndIsEnabled(t *testing.T) {
	Set("org-test", testFlag, true)
	if !IsEnabled("org-test", testFlag) {
		t.Fatal("expected flag to be enabled after Set")
	}
	Set("org-test", testFlag, false)
	if IsEnabled("org-test", testFlag) {
		t.Fatal("expected flag to be disabled after Set(false)")
	}
}

func TestIsEnabled_UnknownFlag(t *testing.T) {
	if IsEnabled("org-1", "does.not.exist") {
		t.Fatal("unknown flag should return false")
	}
}

func TestSnapshotForOrg(t *testing.T) {
	Set("org-snap", testFlag, true)
	snap := SnapshotForOrg("org-snap")
	if !snap[testFlag] {
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
		testFlag:          true,
		"garbage.unknown": true,
	})
	if !IsEnabled("org-all", testFlag) {
		t.Fatal("expected fixture flag enabled via SetAll")
	}
	snap := SnapshotForOrg("org-all")
	if _, found := snap["garbage.unknown"]; found {
		t.Fatal("SetAll should filter out unknown flags")
	}
}

// Released CLIs read a missing experimental.cli_contexts as a gateway that
// predates `hoop context`, and refuse to switch. Graduate it with Default: true.
func TestCLIContextsStaysInCatalog(t *testing.T) {
	if _, ok := Lookup(FlagCLIContexts); !ok {
		t.Fatalf("%s left the catalog: every released `hoop context use` would refuse this gateway", FlagCLIContexts)
	}
}

func TestReplace(t *testing.T) {
	c := NewCache()
	c.Set("org-a", testFlag, true)
	c.Set("org-b", testFlag, true)

	gen := c.Generation()
	changed, ok := c.Replace(gen, map[string]map[string]bool{
		"org-a": {testFlag: true},                       // unchanged
		"org-c": {testFlag: true, "unknown.flag": true}, // new org
	})
	if !ok {
		t.Fatal("Replace refused with no write since Generation")
	}
	if want := []string{"org-b", "org-c"}; !slices.Equal(changed, want) {
		t.Fatalf("changed = %v, want %v", changed, want)
	}
	if !c.IsEnabled("org-a", testFlag) || !c.IsEnabled("org-c", testFlag) {
		t.Fatal("Replace lost a stored value")
	}
	if c.IsEnabled("org-b", testFlag) {
		t.Fatal("an org absent from the snapshot must read the default")
	}
	if _, ok := c.SnapshotForOrg("org-c")["unknown.flag"]; ok {
		t.Fatal("Replace kept a flag the catalog does not know")
	}

	changed, ok = c.Replace(c.Generation(), map[string]map[string]bool{
		"org-a": {testFlag: true}, "org-c": {testFlag: true},
	})
	if !ok || len(changed) != 0 {
		t.Fatalf("same snapshot: changed = %v, ok = %v; want none, true", changed, ok)
	}
}

// A refresh that read the database before a local write must not undo it.
func TestReplaceRefusesASnapshotOlderThanAWrite(t *testing.T) {
	c := NewCache()
	gen := c.Generation()
	c.Set("org-a", testFlag, true)
	if _, ok := c.Replace(gen, map[string]map[string]bool{}); ok {
		t.Fatal("Replace accepted a snapshot read before Set")
	}
	if !c.IsEnabled("org-a", testFlag) {
		t.Fatal("the refused snapshot changed the cache")
	}
	gen = c.Generation()
	c.SetAll("org-a", map[string]bool{})
	if _, ok := c.Replace(gen, map[string]map[string]bool{"org-a": {testFlag: true}}); ok {
		t.Fatal("Replace accepted a snapshot read before SetAll")
	}
}

// Writers, refreshes and readers at once, for -race. A refresh that read
// before a write never lands over it.
func TestCacheConcurrentUse(t *testing.T) {
	c := NewCache()
	orgs := []string{"org-a", "org-b", "org-c"}
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 500 {
				org := orgs[(w+i)%len(orgs)]
				gen := c.Generation()
				if i%2 == 0 {
					c.Set(org, testFlag, i%4 == 0)
				} else {
					c.SetAll(org, map[string]bool{testFlag: true})
				}
				if _, ok := c.Replace(gen, map[string]map[string]bool{org: {testFlag: false}}); ok {
					t.Errorf("Replace applied a snapshot read before a write")
					return
				}
			}
		}()
	}
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 500 {
				org := orgs[i%len(orgs)]
				c.Replace(c.Generation(), map[string]map[string]bool{org: {testFlag: i%2 == 0}})
				_ = c.IsEnabled(org, testFlag)
				for range c.SnapshotForOrg(org) {
				}
			}
		}()
	}
	wg.Wait()

	_, ok := c.Replace(c.Generation(), map[string]map[string]bool{"org-a": {testFlag: true}})
	if !ok {
		t.Fatal("Replace refused with no write since Generation")
	}
	if !c.IsEnabled("org-a", testFlag) || c.IsEnabled("org-b", testFlag) {
		t.Fatal("the last Replace is not the cache")
	}
}
