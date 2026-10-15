package ica

import (
	"context"
	"net"
	"net/netip"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/libhoop-ica/ica"
)

func ptr[T any](v T) *T { return &v }

// singleLane is a single-VDA lane at a loopback listener the test owns, so
// the policy a Serve would derive is 127.0.0.1/32 on that port.
func singleLane(t *testing.T) daemon.ICALane {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return daemon.ICALane{Name: "vda", Listen: "127.0.0.1:0", TenantID: "acme", Upstream: ln.Addr().String()}
}

func TestBuildRefusesWhatTheEngineRefuses(t *testing.T) {
	cases := []struct {
		name string
		rt   daemon.ICARuntime
		lane daemon.ICALane
		want string
	}{
		{"contradictory options", daemon.ICARuntime{Options: daemon.ICAOptions{OutputPipeline: ptr(false)}},
			singleLane(t), "ica options: adaptive-coalescing requires output-pipeline"},
		{"thread limit out of range", daemon.ICARuntime{Options: daemon.ICAOptions{OCRThreadLimit: ptr(17)}},
			singleLane(t), "ica options: ocr-thread-limit must be 0..16"},
		{"missing tenant", daemon.ICARuntime{},
			daemon.ICALane{Name: "vda", Upstream: "10.0.0.1:1494"}, "ica.tenant_id is required"},
		{"cidr without prefixes", daemon.ICARuntime{},
			daemon.ICALane{Name: "vda", TenantID: "acme", Transparent: true, SourcePrefixes: []netip.Prefix{netip.MustParsePrefix("10.1.0.0/16")}},
			"ica.transparent.upstream_cidrs is empty"},
		{"single without port", daemon.ICARuntime{},
			daemon.ICALane{Name: "vda", TenantID: "acme", Upstream: "vda.example"}, "upstream must be host:port"},
		{"single with port zero", daemon.ICARuntime{},
			daemon.ICALane{Name: "vda", TenantID: "acme", Upstream: "vda.example:0"}, "port must be 1..65535"},
	}
	if !ica.ORTCompiled {
		cases = append(cases, struct {
			name string
			rt   daemon.ICARuntime
			lane daemon.ICALane
			want string
		}{"ort on a non-ORT build", daemon.ICARuntime{Options: daemon.ICAOptions{NERBackend: ptr("ort")}},
			singleLane(t), "ica options: ner-backend ort is not compiled into this build"})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, err := Build(tc.rt, tc.lane)
			if err == nil {
				t.Fatalf("built %T, want error %q", srv, tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestBuildIsInertAndTheServerIsIdleBeforeServe(t *testing.T) {
	srv, err := Build(daemon.ICARuntime{}, singleLane(t))
	if err != nil {
		t.Fatal(err)
	}
	if srv.Ready() {
		t.Fatal("ready before Serve")
	}
	if srv.Addr() != nil {
		t.Fatalf("addr %v before Serve", srv.Addr())
	}
	if a, n, d := srv.Stats(); a != 0 || n != 0 || d != 0 {
		t.Fatalf("stats %d/%d/%d before Serve", a, n, d)
	}
	if got := srv.Sessions(); len(got) != 0 {
		t.Fatalf("sessions %v before Serve", got)
	}
	if srv.Stop("nope") {
		t.Fatal("stopped a session that cannot exist")
	}
	// Close before Serve does nothing and leaves no runtime behind; a
	// second Close is a no-op too.
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	runtimesMu.Lock()
	defer runtimesMu.Unlock()
	if len(runtimes) != 0 {
		t.Fatalf("%d runtimes loaded by Build/Close", len(runtimes))
	}
}

func TestServeAfterCloseDoesNotLoad(t *testing.T) {
	srv, err := Build(daemon.ICARuntime{}, singleLane(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	// A live ctx: had Serve reached the load, it would have tried to spawn
	// OCR workers from the profile's path and failed loudly.
	if err := srv.Serve(context.Background()); err != nil {
		t.Fatalf("Serve after Close: %v", err)
	}
	if srv.Ready() || srv.Addr() != nil {
		t.Fatal("Serve after Close started something")
	}
	runtimesMu.Lock()
	defer runtimesMu.Unlock()
	if len(runtimes) != 0 {
		t.Fatalf("%d runtimes loaded by Serve after Close", len(runtimes))
	}
}

func TestNotesStateModeAndResolvedRuntime(t *testing.T) {
	rt := daemon.ICARuntime{OCRWorker: "/w", Tessdata: "/t", NERModel: "/m",
		Options: daemon.ICAOptions{OCRWorkers: ptr(3), NERBackend: ptr("go"), PIIEntities: []string{"EMAIL", "PHONE"}, OutputLatestFrame: ptr(false)}}
	srv, err := Build(rt, singleLane(t))
	if err != nil {
		t.Fatal(err)
	}
	notes := strings.Join(srv.Notes(), "\n")
	for _, want := range []string{
		"ocr tesseract, 3 workers, 8 tiles, thread limit 1",
		"ner backend go (onnx runtime compiled in: " + map[bool]string{true: "true", false: "false"}[ica.ORTCompiled] + ")",
		"pii threshold 0.80, entities EMAIL,PHONE",
		"latest frame false",
		"ocr worker /w, tessdata /t, ner model /m, ort library " + ica.DefaultNERORTLibrary,
	} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}

	cidr := daemon.ICALane{Name: "gw", Listen: "0.0.0.0:2598", TenantID: "acme", Transparent: true,
		SourcePrefixes:   []netip.Prefix{netip.MustParsePrefix("10.1.0.0/16")},
		UpstreamPrefixes: []netip.Prefix{netip.MustParsePrefix("10.2.0.0/16")}, UpstreamPorts: []uint16{1494, 2598}}
	srv, err = Build(daemon.ICARuntime{}, cidr)
	if err != nil {
		t.Fatal(err)
	}
	// The mode itself is the daemon's note; the adapter adds only what the
	// daemon cannot know — here, that this host cannot serve CIDR mode.
	notes = strings.Join(srv.Notes(), "\n")
	if strings.Contains(notes, "CIDR mode; the VDA") || strings.Contains(notes, "single-VDA mode") {
		t.Errorf("the adapter repeats the daemon's mode note:\n%s", notes)
	}
	hostNote := "cannot serve CIDR mode"
	if strings.Contains(notes, hostNote) != (runtime.GOOS != "linux") {
		t.Errorf("host note presence wrong for %s:\n%s", runtime.GOOS, notes)
	}
}

func TestResolveRuntimeConfigAppliesEveryOverride(t *testing.T) {
	profile := ica.DefaultRuntimeConfig()
	rt := daemon.ICARuntime{OCRWorker: "/ocr", Tessdata: "/tess", NERModel: "/ner", NERORTLibrary: "/ort.so",
		Options: daemon.ICAOptions{
			OCRWorkers: ptr(3), OCRTiles: ptr(2), OCRThreadLimit: ptr(4), OCRTimeoutSec: ptr(7), OCRMinConfidence: ptr(0.5),
			OCRBrowserOptimizations: ptr(!profile.OCRBrowserOptimizations), OCRScreenOptimizations: ptr(!profile.OCRScreenOptimizations),
			OCRGrayPreparation: ptr(!profile.OCRGrayPreparation), OCRNoInvert: ptr(!profile.OCRNoInvert),
			OCRFreshReuseFrames: ptr(9), OCRLineBandScale: ptr(!profile.OCRLineBandScale), OCRLineBandMinRows: ptr(11),
			NERBackend: ptr("go"), NERThreads: ptr(5), NERParallel: ptr(6), PIIThreshold: ptr(0.33), PIIEntities: []string{"EMAIL"},
			VideoBatch: ptr(!profile.VideoBatch), OutputPipeline: ptr(!profile.OutputPipeline), AdaptiveCoalescing: ptr(!profile.AdaptiveCoalescing),
			PresentationV2: ptr(!profile.PresentationV2), PresentationScreenCopy: ptr(!profile.PresentationScreenCopy), OutputLatestFrame: ptr(!profile.OutputLatestFrame),
		}}
	got := resolveRuntimeConfig(rt)
	checks := []struct {
		name      string
		got, want any
	}{
		{"OCRWorker", got.OCRWorker, "/ocr"}, {"Tessdata", got.Tessdata, "/tess"}, {"NERModel", got.NERModel, "/ner"}, {"NERORTLibrary", got.NERORTLibrary, "/ort.so"},
		{"OCRWorkers", got.OCRWorkers, 3}, {"OCRTiles", got.OCRTiles, 2}, {"OCRThreadLimit", got.OCRThreadLimit, 4},
		{"OCRTimeout", got.OCRTimeout, 7 * time.Second}, {"OCRMinConfidence", got.OCRMinConfidence, 0.5},
		{"OCRBrowserOptimizations", got.OCRBrowserOptimizations, !profile.OCRBrowserOptimizations},
		{"OCRScreenOptimizations", got.OCRScreenOptimizations, !profile.OCRScreenOptimizations},
		{"OCRGrayPreparation", got.OCRGrayPreparation, !profile.OCRGrayPreparation}, {"OCRNoInvert", got.OCRNoInvert, !profile.OCRNoInvert},
		{"OCRFreshReuseFrames", got.OCRFreshReuseFrames, 9}, {"OCRLineBandScale", got.OCRLineBandScale, !profile.OCRLineBandScale},
		{"OCRLineBandMinRows", got.OCRLineBandMinRows, 11}, {"NERBackend", got.NERBackend, "go"}, {"NERThreads", got.NERThreads, 5},
		{"NERParallel", got.NERParallel, 6}, {"PIIThreshold", got.PIIThreshold, 0.33}, {"PIIEntities", strings.Join(got.PIIEntities, ","), "EMAIL"},
		{"VideoBatch", got.VideoBatch, !profile.VideoBatch}, {"OutputPipeline", got.OutputPipeline, !profile.OutputPipeline},
		{"AdaptiveCoalescing", got.AdaptiveCoalescing, !profile.AdaptiveCoalescing}, {"PresentationV2", got.PresentationV2, !profile.PresentationV2},
		{"PresentationScreenCopy", got.PresentationScreenCopy, !profile.PresentationScreenCopy}, {"OutputLatestFrame", got.OutputLatestFrame, !profile.OutputLatestFrame},
		// Not on the override surface.
		{"OCRBackend", got.OCRBackend, "tesseract"}, {"BitmapDecoder", got.BitmapDecoder, "independent"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if got.FailureDiagnostics != nil {
		t.Error("FailureDiagnostics opened at Build")
	}

	// No overrides: the profile, untouched, including the entity set.
	plain := resolveRuntimeConfig(daemon.ICARuntime{})
	if plain.OCRWorker != profile.OCRWorker || plain.OCRWorkers != profile.OCRWorkers || plain.NERBackend != profile.NERBackend ||
		strings.Join(plain.PIIEntities, ",") != strings.Join(profile.PIIEntities, ",") {
		t.Errorf("no overrides changed the profile: %+v", plain)
	}
	// An empty entity list keeps the profile's, so an operator cannot fall
	// to the base profile by accident.
	kept := resolveRuntimeConfig(daemon.ICARuntime{Options: daemon.ICAOptions{PIIEntities: []string{}}})
	if strings.Join(kept.PIIEntities, ",") != strings.Join(profile.PIIEntities, ",") {
		t.Errorf("empty pii_entities replaced the profile's: %v", kept.PIIEntities)
	}
}

func TestFingerprintSharesIdenticalConfigs(t *testing.T) {
	a := resolveRuntimeConfig(daemon.ICARuntime{Options: daemon.ICAOptions{PIIEntities: []string{"EMAIL", "PHONE"}}})
	b := resolveRuntimeConfig(daemon.ICARuntime{Options: daemon.ICAOptions{PIIEntities: []string{"PHONE", "EMAIL"}}})
	if fingerprint(a, "") != fingerprint(b, "") {
		t.Error("entity order split two identical configs")
	}
	c := resolveRuntimeConfig(daemon.ICARuntime{Options: daemon.ICAOptions{OCRWorkers: ptr(2)}})
	if fingerprint(a, "") == fingerprint(c, "") {
		t.Error("different options share a fingerprint")
	}
	if fingerprint(a, "") == fingerprint(a, "/var/lib/ica") {
		t.Error("the failure diagnostics directory is not part of the fingerprint")
	}
}

func TestSinglePolicyAdmitsExactlyTheUpstream(t *testing.T) {
	cases := []struct {
		upstream, prefix, fixed string
	}{
		{"127.0.0.1:1494", "127.0.0.1/32", "127.0.0.1:1494"},
		{"[::1]:2598", "::1/128", "[::1]:2598"},
		{"[::ffff:10.0.0.9]:1494", "10.0.0.9/32", "10.0.0.9:1494"},
	}
	for _, tc := range cases {
		addr, port, err := resolveUpstream(context.Background(), tc.upstream)
		if err != nil {
			t.Fatalf("%s: %v", tc.upstream, err)
		}
		policy, fixed := singlePolicy(addr, port)
		if fixed != tc.fixed {
			t.Errorf("%s: fixed destination %s, want %s", tc.upstream, fixed, tc.fixed)
		}
		if len(policy.Prefixes) != 1 || policy.Prefixes[0].String() != tc.prefix {
			t.Errorf("%s: prefixes %v, want [%s]", tc.upstream, policy.Prefixes, tc.prefix)
		}
		if _, ok := policy.Ports[port]; !ok || len(policy.Ports) != 1 {
			t.Errorf("%s: ports %v, want only %d", tc.upstream, policy.Ports, port)
		}
		if err := policy.ValidateUpstream(fixed); err != nil {
			t.Errorf("%s: the engine refuses its own destination: %v", tc.upstream, err)
		}
		if err := policy.ValidateUpstream(net.JoinHostPort(addr.String(), "9")); err == nil {
			t.Errorf("%s: another port admitted", tc.upstream)
		}
	}
}

func TestResolveUpstreamResolvesAHostnameOnce(t *testing.T) {
	addr, port, err := resolveUpstream(context.Background(), "localhost:1494")
	if err != nil {
		t.Skipf("localhost does not resolve here: %v", err)
	}
	if !addr.IsLoopback() || port != 1494 {
		t.Fatalf("localhost:1494 resolved to %s:%d", addr, port)
	}
	if _, _, err := resolveUpstream(context.Background(), "no-such-host.invalid:1494"); err == nil {
		t.Fatal("an unresolvable name resolved")
	}
}

func TestCIDRPolicyAndSourceFallback(t *testing.T) {
	lane := daemon.ICALane{Transparent: true, UpstreamPrefixes: []netip.Prefix{netip.MustParsePrefix("10.2.0.0/16")}, UpstreamPorts: []uint16{1494}}
	policy := cidrPolicy(lane)
	if err := policy.ValidateUpstream("10.2.3.4:1494"); err != nil {
		t.Error(err)
	}
	if err := policy.ValidateUpstream("10.2.3.4:2598"); err == nil {
		t.Error("a port outside upstream_ports admitted")
	}
	lane.UpstreamPorts = nil
	if err := cidrPolicy(lane).ValidateUpstream("10.2.3.4:2598"); err != nil {
		t.Errorf("no port list should allow every port: %v", err)
	}
	for _, src := range []string{"203.0.113.9", "2001:db8::1"} {
		if !anySource[0].Contains(netip.MustParseAddr(src)) && !anySource[1].Contains(netip.MustParseAddr(src)) {
			t.Errorf("single mode's any-source policy does not admit %s", src)
		}
	}
}

func TestObserveCountsDenialsAndForwards(t *testing.T) {
	var got []daemon.ICAEvent
	s := &server{lane: daemon.ICALane{Events: func(e daemon.ICAEvent) { got = append(got, e) }}}
	now := time.Now()
	s.observe(ica.Event{Time: now, Event: ica.EventConnectionRejected, Data: map[string]any{"stage": ica.RejectStageGatewaySource, "decision": "block"}})
	s.observe(ica.Event{Time: now, Event: ica.EventSessionEnd, SessionID: "s1", Data: map[string]any{"decision": ica.DecisionBlock}})
	s.observe(ica.Event{Time: now, Event: ica.EventSessionEnd, SessionID: "s2", Data: map[string]any{"decision": ica.DecisionRelease}})
	s.observe(ica.Event{Time: now, Event: ica.EventFrameDecision, SessionID: "s2", Direction: "vda_to_client"})
	if _, _, denied := s.Stats(); denied != 2 {
		t.Fatalf("denied %d, want 2 (one rejection, one blocked session)", denied)
	}
	if len(got) != 4 || got[1].Name != ica.EventSessionEnd || got[1].SessionID != "s1" || got[3].Direction != "vda_to_client" || !got[0].Time.Equal(now) {
		t.Fatalf("forwarded events %+v", got)
	}
	// No bridge: counting still works.
	quiet := &server{}
	quiet.observe(ica.Event{Event: ica.EventConnectionRejected})
	if _, _, denied := quiet.Stats(); denied != 1 {
		t.Fatalf("denied %d without a bridge", denied)
	}
}
