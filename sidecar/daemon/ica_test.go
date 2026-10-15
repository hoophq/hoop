package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
)

// withICAEngine installs an engine builder for one test. RegisterICAEngine
// panics on a second registration into the process-wide registry, and a
// test needs to replace the engine, so withICAEngine swaps the builder under
// the lock and restores it on cleanup. Tests using it must not run in
// parallel with tests that read the registry.
func withICAEngine(t *testing.T, build ICABuilder) {
	t.Helper()
	icaMu.Lock()
	prev := icaBuild
	icaBuild = build
	icaMu.Unlock()
	t.Cleanup(func() {
		icaMu.Lock()
		icaBuild = prev
		icaMu.Unlock()
	})
}

// fakeICAServer is the server a test engine returns. It records the lane
// and runtime it was built with and returns fixed values from the admin
// methods.
type fakeICAServer struct {
	rt    ICARuntime
	lane  ICALane
	ready bool
	notes []string
}

func (f *fakeICAServer) Serve(ctx context.Context) error { <-ctx.Done(); return nil }
func (f *fakeICAServer) Close() error                    { return nil }
func (f *fakeICAServer) Addr() net.Addr                  { return nil }
func (f *fakeICAServer) Stats() (int64, int64, int64)    { return 1, 2, 3 }
func (f *fakeICAServer) Notes() []string                 { return f.notes }
func (f *fakeICAServer) Ready() bool                     { return f.ready }

func fakeICAEngine(ready bool) (ICABuilder, *[]*fakeICAServer) {
	var built []*fakeICAServer
	return func(rt ICARuntime, lane ICALane) (ICAServer, error) {
		s := &fakeICAServer{rt: rt, lane: lane, ready: ready, notes: []string{"engine: fake"}}
		built = append(built, s)
		return s, nil
	}, &built
}

const icaSingle = `{"name":"vdi","protocol":"ica","listen":"127.0.0.1:2598","upstream":"10.77.0.30:2598","ica":{"tenant_id":"finance"}}`
const icaCIDR = `{"name":"vdi","protocol":"ica","listen":"0.0.0.0:2598","ica":{"tenant_id":"prod",
  "transparent":{"source_cidrs":["10.10.1.10/32"],"upstream_cidrs":["10.77.0.0/16"],"upstream_ports":[2598]},
  "display":{"width":1264,"height":1040,"thinwire_channel":9}}}`

func TestICAListenerValidation(t *testing.T) {
	cases := map[string]struct {
		doc  string
		want string // "" = loads
	}{
		"single mode":          {`{"listeners":[` + icaSingle + `]}`, ""},
		"cidr mode":            {`{"listeners":[` + icaCIDR + `]}`, ""},
		"ports as strings":     {`{"listeners":[{"name":"v","protocol":"ica","listen":":1","ica":{"tenant_id":"t","transparent":{"source_cidrs":["10.0.0.0/8"],"upstream_cidrs":["10.0.0.0/8"],"upstream_ports":["2598","1494"]}}}]}`, ""},
		"both modes":           {`{"listeners":[{"name":"v","protocol":"ica","listen":":1","upstream":"10.0.0.1:2598","ica":{"tenant_id":"t","transparent":{"source_cidrs":["10.0.0.0/8"],"upstream_cidrs":["10.0.0.0/8"],"upstream_ports":[2598]}}}]}`, "two modes"},
		"neither mode":         {`{"listeners":[{"name":"v","protocol":"ica","listen":":1","ica":{"tenant_id":"t"}}]}`, "names a single VDA in upstream"},
		"no block":             {`{"listeners":[{"name":"v","protocol":"ica","listen":":1","upstream":"10.0.0.1:2598"}]}`, `needs an "ica" block`},
		"no tenant":            {`{"listeners":[{"name":"v","protocol":"ica","listen":":1","upstream":"10.0.0.1:2598","ica":{}}]}`, "tenant_id is required"},
		"cidr without ports":   {`{"listeners":[{"name":"v","protocol":"ica","listen":":1","ica":{"tenant_id":"t","transparent":{"source_cidrs":["10.0.0.0/8"],"upstream_cidrs":["10.0.0.0/8"]}}}]}`, "upstream_ports is required"},
		"cidr bad cidr":        {`{"listeners":[{"name":"v","protocol":"ica","listen":":1","ica":{"tenant_id":"t","transparent":{"source_cidrs":["10.0.0.0"],"upstream_cidrs":["10.0.0.0/8"],"upstream_ports":[2598]}}}]}`, "is not a CIDR"},
		"cidr bad port":        {`{"listeners":[{"name":"v","protocol":"ica","listen":":1","ica":{"tenant_id":"t","transparent":{"source_cidrs":["10.0.0.0/8"],"upstream_cidrs":["10.0.0.0/8"],"upstream_ports":["cgp"]}}}]}`, "is not a port"},
		"cidr on unix":         {`{"listeners":[{"name":"v","protocol":"ica","network":"unix","listen":"/tmp/x.sock","ica":{"tenant_id":"t","transparent":{"source_cidrs":["10.0.0.0/8"],"upstream_cidrs":["10.0.0.0/8"],"upstream_ports":[2598]}}}]}`, "network: unix cannot carry it"},
		"cidr blank source":    {`{"listeners":[{"name":"v","protocol":"ica","listen":":1","ica":{"tenant_id":"t","transparent":{"source_cidrs":[" "],"upstream_cidrs":["10.0.0.0/8"],"upstream_ports":[2598]}}}]}`, "source_cidrs: an entry is blank"},
		"cidr blank vda":       {`{"listeners":[{"name":"v","protocol":"ica","listen":":1","ica":{"tenant_id":"t","transparent":{"source_cidrs":["10.0.0.0/8"],"upstream_cidrs":["10.0.0.0/8",""],"upstream_ports":[2598]}}}]}`, "upstream_cidrs: an entry is blank"},
		"display range":        {`{"listeners":[{"name":"v","protocol":"ica","listen":":1","upstream":"10.0.0.1:2598","ica":{"tenant_id":"t","display":{"width":100}}}]}`, "width must be 320..4096"},
		"upstream tls":         {`{"listeners":[{"name":"v","protocol":"ica","listen":":1","upstream":"10.0.0.1:2598","upstream_tls":{},"ica":{"tenant_id":"t"}}]}`, "upstream_tls is not valid on an ica listener"},
		"identity header":      {`{"listeners":[{"name":"v","protocol":"ica","listen":":1","upstream":"10.0.0.1:2598","identity_header":"x","ica":{"tenant_id":"t"}}]}`, "identity_header is only supported"},
		"block elsewhere":      {`{"listeners":[{"name":"db","protocol":"postgres","listen":":1","upstream":"h:1","ica":{"tenant_id":"t"}}]}`, `an "ica" block is only valid on an ica listener`},
		"own guardrails":       {`{"listeners":[{"name":"v","protocol":"ica","listen":":1","upstream":"10.0.0.1:2598","ica":{"tenant_id":"t"},"guardrails":{"rules":[{"name":"r","type":"operation","operations":["select"]}]}}]}`, "guardrail rules do not apply"},
		"inherited guardrails": {`{"guardrails":{"rules":[{"name":"r","type":"operation","operations":["select"]}]},"listeners":[` + icaSingle + `]}`, "guardrail rules do not apply"},
		"opted out guardrails": {`{"guardrails":{"rules":[{"name":"r","type":"operation","operations":["select"]}]},"listeners":[{"name":"v","protocol":"ica","listen":":1","upstream":"10.0.0.1:2598","ica":{"tenant_id":"t"},"guardrails":{"rules":[]}}]}`, ""},
		"mask rules":           {`{"listeners":[{"name":"v","protocol":"ica","listen":":1","upstream":"10.0.0.1:2598","ica":{"tenant_id":"t"},"mask":{"rules":[{"name":"emails","entities":["EMAIL_ADDRESS"]}]}}]}`, "mask rules do not apply"},
		"analyzer":             {`{"listeners":[{"name":"v","protocol":"ica","listen":":1","upstream":"10.0.0.1:2598","ica":{"tenant_id":"t"},"analyzer":{"high":"block"}}]}`, "analyzer does not apply"},
		"opa":                  {`{"opa":{"url":"http://opa/v1/data/x"},"listeners":[` + icaSingle + `]}`, "opa does not apply"},
		"opa opted out":        {`{"opa":{"url":"http://opa/v1/data/x"},"listeners":[{"name":"v","protocol":"ica","listen":":1","upstream":"10.0.0.1:2598","ica":{"tenant_id":"t"},"opa":{}}]}`, ""},
		"runtime ranges":       {`{"ica":{"max_sessions":5000},"listeners":[` + icaSingle + `]}`, "max_sessions must be 1..4096"},
		"option range":         {`{"ica":{"options":{"ocr_tiles":17}},"listeners":[` + icaSingle + `]}`, "ocr_tiles must be 1..16"},
		"option enum":          {`{"ica":{"options":{"ner_backend":"cuda"}},"listeners":[` + icaSingle + `]}`, "ner_backend must be go or ort"},
		"option threshold":     {`{"ica":{"options":{"pii_threshold":1.5}},"listeners":[` + icaSingle + `]}`, "pii_threshold must be 0..1"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			// CheckConfigBytes runs the plane's check, with no engine and no
			// host. Each ica rule above must hold there, because the plane is
			// the first place to refuse an operator's edit.
			err := CheckConfigBytes([]byte(tc.doc))
			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("refused: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

// Config validation checks an explicit asset path only on the host, where
// the engine reads the file. The plane holds no files, and the image lays
// out the default paths.
func TestICARuntimePathsAreCheckedOnTheHostOnly(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	doc := `{"ica":{"ocr_worker":"` + missing + `"},"listeners":[` + icaSingle + `]}`
	if err := CheckConfigBytes([]byte(doc)); err != nil {
		t.Fatalf("the plane refused a path only the host can check: %v", err)
	}
	cfg, err := LoadConfigBytes([]byte(doc))
	if err == nil {
		t.Fatalf("the host accepted a missing ocr_worker: %+v", cfg.ICA)
	}
	if !strings.Contains(err.Error(), "ica.ocr_worker") {
		t.Fatalf("wrong problem: %v", err)
	}
	if _, err := LoadConfigBytes([]byte(`{"listeners":[` + icaSingle + `]}`)); err != nil {
		t.Fatalf("a config with no explicit path must load without the assets installed: %v", err)
	}
}

// Without an engine the document is valid (the plane accepts it) but the
// process cannot serve it; with one, Validate constructs the server and
// reports its notes beside the lane's own.
func TestICAValidateNeedsAnEngineAndReportsNotes(t *testing.T) {
	cfg, err := LoadConfigBytes([]byte(`{"listeners":[` + icaCIDR + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(cfg, nil); err == nil || !strings.Contains(err.Error(), "did not link") {
		t.Fatalf("ica lane accepted with no engine: %v", err)
	}
	build, built := fakeICAEngine(true)
	withICAEngine(t, build)
	info, err := Validate(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(info) != 1 || info[0].Protocol != "ica" {
		t.Fatalf("lane info: %+v", info)
	}
	notes := strings.Join(info[0].Notes, "\n")
	for _, want := range []string{"engine: fake", "CIDR mode", "CAP_NET_ADMIN"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes lack %q:\n%s", want, notes)
		}
	}
	if len(*built) != 1 {
		t.Fatalf("engine built %d times", len(*built))
	}
	lane := (*built)[0].lane
	if !lane.Transparent || lane.TenantID != "prod" || lane.Width != 1264 || lane.ThinwireChannel != 9 ||
		len(lane.SourcePrefixes) != 1 || lane.SourcePrefixes[0] != netip.MustParsePrefix("10.10.1.10/32") ||
		len(lane.UpstreamPorts) != 1 || lane.UpstreamPorts[0] != 2598 || lane.Events == nil || lane.Log == nil {
		t.Fatalf("resolved lane: %+v", lane)
	}
}

func TestICAResolvesSingleModeAndRuntimeOverrides(t *testing.T) {
	cfg, err := LoadConfigBytes([]byte(`{"ica":{"max_sessions":8,"options":{"ocr_workers":4,"video_batch":false,"pii_entities":["EMAIL_ADDRESS"]}},"listeners":[` + icaSingle + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	build, built := fakeICAEngine(true)
	withICAEngine(t, build)
	if _, err := Validate(cfg, nil); err != nil {
		t.Fatal(err)
	}
	s := (*built)[0]
	if s.lane.Transparent || s.lane.Upstream != "10.77.0.30:2598" || len(s.lane.SourcePrefixes) != 0 || s.lane.TenantID != "finance" {
		t.Fatalf("single-mode lane: %+v", s.lane)
	}
	if s.rt.MaxSessions != 8 || s.rt.Options.OCRWorkers == nil || *s.rt.Options.OCRWorkers != 4 ||
		s.rt.Options.VideoBatch == nil || *s.rt.Options.VideoBatch || s.rt.Options.OCRTiles != nil ||
		len(s.rt.Options.PIIEntities) != 1 {
		t.Fatalf("runtime overrides: %+v", s.rt)
	}
}

// The bridge writes one row per lifecycle event and none per frame. A
// refused connection is a violation with the stage as the rule; a blocked
// session is a violation that also ends the session; the end row carries
// the counts.
func TestICAAuditBridgeShapesTheTrail(t *testing.T) {
	sink := audit.NewMemorySink(16)
	b := newICAAuditBridge(sink, "vdi", "finance", false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	at := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	ev := func(name, id string, data map[string]any, offset time.Duration) ICAEvent {
		return ICAEvent{Time: at.Add(offset), Name: name, SessionID: id, Data: data}
	}
	b.observe(ev(icaEventConnectionRejected, "", map[string]any{"stage": "gateway_source_policy", "decision": "block", "error": "source 10.9.9.9 is not a Gateway"}, 0))
	b.observe(ev(icaEventSessionStart, "inline-1", map[string]any{"transport": "tcp"}, time.Second))
	b.observe(ev(icaEventFrameDecision, "inline-1", nil, 2*time.Second))
	b.observe(ev(icaEventFrameDecision, "inline-1", nil, 3*time.Second))
	b.observe(ev(icaEventOCRResult, "inline-1", nil, 3*time.Second))
	b.observe(ev(icaEventSessionEnd, "inline-1", map[string]any{"decision": "release"}, 10*time.Second))
	b.observe(ev(icaEventSessionStart, "inline-2", nil, 11*time.Second))
	b.observe(ev(icaEventSessionEnd, "inline-2", map[string]any{"decision": "block"}, 12*time.Second))
	b.observe(ev("libhoop_ica.edt_sender_stats", "inline-2", map[string]any{"stats": "..."}, 12*time.Second))

	rows := sink.Events()
	kinds := make([]string, len(rows))
	for i, r := range rows {
		kinds[i] = string(r.Kind)
		if r.Protocol != inspect.ICA || r.Connection != "vdi" || r.Principal != "finance" {
			t.Fatalf("row %d is not labelled as this lane's: %+v", i, r)
		}
		if strings.Contains(strings.ToLower(r.Statement), "stats") || r.Statement != "" {
			t.Fatalf("row %d carries content: %+v", i, r)
		}
	}
	if want := []string{"violation", "session_start", "session_end", "session_start", "violation"}; strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("rows %v, want %v", kinds, want)
	}
	if rows[0].Rule != "gateway_source_policy" || rows[0].SessionID != "" || rows[0].Message == "" {
		t.Fatalf("rejection row: %+v", rows[0])
	}
	if rows[2].Duration != 9*time.Second || rows[2].StatementCount != 2 || rows[2].Metadata["frames"] != "2" || rows[2].Metadata["ocr_batches"] != "1" || !rows[2].Allowed {
		t.Fatalf("release row: %+v", rows[2])
	}
	if rows[4].Allowed || rows[4].DeniedCount != 1 || rows[4].Rule != "block" {
		t.Fatalf("block row: %+v", rows[4])
	}
}

// A fail-closed lane must not run a session the trail could not record:
// the bridge reports the failed start so the adapter stops the session.
// A fail-open lane logs and carries on, and only the start counts, because
// the rows after it describe a session that already ran.
func TestICAAuditBridgeFailClosedStopsUnrecordedSessions(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	start := ICAEvent{Name: icaEventSessionStart, SessionID: "s1"}
	end := ICAEvent{Name: icaEventSessionEnd, SessionID: "s1", Data: map[string]any{"decision": "release"}}
	rejected := ICAEvent{Name: icaEventConnectionRejected, Data: map[string]any{"stage": "gateway_source_policy"}}

	closed := newICAAuditBridge(brokenSink{}, "vdi", "t", true, quiet)
	if err := closed.observe(start); !errors.Is(err, errICAUnaudited) {
		t.Fatalf("fail-closed start: err = %v, want errICAUnaudited", err)
	}
	if err := closed.observe(rejected); err != nil {
		t.Fatalf("fail-closed rejection: err = %v, want nil (no session to stop)", err)
	}
	if err := closed.observe(end); err != nil {
		t.Fatalf("fail-closed end: err = %v, want nil (the session is over)", err)
	}

	open := newICAAuditBridge(brokenSink{}, "vdi", "t", false, quiet)
	if err := open.observe(start); err != nil {
		t.Fatalf("fail-open start: err = %v, want nil", err)
	}
}

type brokenSink struct{}

func (brokenSink) Write(context.Context, audit.Event) error { return errors.New("disk full") }
func (brokenSink) Close() error                             { return nil }

// /readyz consults only the endpoints that report readiness and names the
// ones still loading.
func TestICAReadiness(t *testing.T) {
	loaded := &fakeICAServer{ready: true}
	loading := &fakeICAServer{ready: false}
	plain := struct{ endpointServer }{} // grpc/ssh shape: no Ready
	got := notReadyEndpoints([]endpointServer{loaded, plain, loading}, []string{"a", "b", "c"})
	if strings.Join(got, ",") != "c" {
		t.Fatalf("not ready: %v", got)
	}
}

func TestICAPortListDecodesNumbersAndStrings(t *testing.T) {
	var p ICAPortList
	if err := json.Unmarshal([]byte(`[2598, "1494"]`), &p); err != nil {
		t.Fatal(err)
	}
	ports, err := p.ports()
	if err != nil || len(ports) != 2 || ports[0] != 2598 || ports[1] != 1494 {
		t.Fatalf("%v %v", ports, err)
	}
	if err := json.Unmarshal([]byte(`[true]`), &p); err == nil {
		t.Fatal("a boolean decoded as a port")
	}
}
