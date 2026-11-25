package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hoophq/hoop/sidecar/analyzer"
	"github.com/hoophq/hoop/sidecar/policy"
)

// patternLane is a lane kind a resource pattern reads, with a pattern
// policy.ValidRulePattern refuses and one it accepts. A "*" stands for one
// whole segment, and a gRPC service name is one segment. codec is what an
// analyzer and an http_header rule need on the lane.
type patternLane struct {
	protocol, upstream, codec, bad, good string
}

var patternLanes = []patternLane{
	{"grpc", "h:1", `"grpc":{"raw":{"string_fields":[{"method":"/repro.v1.Alpha/Upload","response":["2"]}]},` +
		`"capture_payload":true}`, "/repro.v1.*/Upload", "/repro.v1.Alpha/*"},
	{"http", "h:80", `"http":{"headers":["x-tenant"]}`, "/api/v1*/users", "/api/v1/**"},
}

// patternSite is one place a config holds a resource pattern that feeds
// policy.MatchResource. In listener, top and want, %[1]s is the pattern.
// top is a top-level block; httpOnly marks a block only an http lane takes.
type patternSite struct {
	name, listener, top, want string
	httpOnly                  bool
}

var patternSites = []patternSite{
	{
		name:     "http_resource rule",
		listener: `"guardrails":{"rules":[{"name":"deny-upload","type":"http_resource","resources":["%[1]s"]}]}`,
		want:     `deny-upload: resource "%[1]s" matches no request as written: segment `,
	},
	{
		name: "http_header rule scope",
		listener: `"guardrails":{"rules":[{"name":"deny-upload","type":"http_header",` +
			`"resources":["%[1]s"],"headers":{"x-tenant":[]}}]}`,
		want: `deny-upload: resource "%[1]s" matches no request as written: segment `,
	},
	{
		name:     "analyzer trigger.resources",
		listener: `"analyzer":{"trigger":{"resources":["%[1]s"]},"high":"block"}`,
		want:     `trigger.resources "%[1]s" matches no request as written: segment `,
	},
	{
		name:     "analyzer trigger.any",
		listener: `"analyzer":{"trigger":{"any":[{"resources":["%[1]s"]}]},"high":"block"}`,
		want:     `trigger.any[0].resources "%[1]s" matches no request as written: segment `,
	},
	{
		name: "analyzer trigger.exclude",
		listener: `"analyzer":{"trigger":{"resources":["/ok/*"],"exclude":[{"resources":["%[1]s"]}]},` +
			`"high":"block"}`,
		want: `trigger.exclude[0].resources "%[1]s" matches no request as written: segment `,
	},
	{
		name: "ai_analysis rule trigger",
		listener: `"guardrails":{"rules":[{"name":"risky","type":"ai_analysis",` +
			`"trigger":{"resources":["%[1]s"]},"high":"block"}]}`,
		want: `ai_analysis rule "risky": trigger.resources "%[1]s" matches no request as written: segment `,
	},
	{
		// At build a library rule that fails to compile is dropped with a
		// note, so the config check is the only place it can be refused.
		name: "rule_library guardrail",
		top:  `"rule_library":{"guardrails":[{"name":"deny-upload","type":"http_resource","resources":["%[1]s"]}]}`,
		want: `rule_library.guardrails[0]: deny-upload: resource "%[1]s" matches no request as written: segment `,
	},
	{
		name: "match_ignore",
		listener: `"analyzer":{"trigger":{"resources":["/ok/*"]},"high":"require_review","approval_rule":"r",` +
			`"match_ignore":[{"resources":["%[1]s"],"fields":[["metadata","annotations"]]}]}`,
		want:     `match_ignore[0] resources[0] "%[1]s" matches no request as written: segment `,
		httpOnly: true,
	},
}

// doc is a sidecar config with one listener of this kind, carrying the
// site's blocks with pattern in them. A zero site gives the bare lane.
func (l patternLane) doc(site patternSite, pattern string) string {
	top, listener := "", ""
	if site.top != "" {
		top = fmt.Sprintf(site.top, pattern) + ","
	}
	if site.listener != "" {
		listener = "," + fmt.Sprintf(site.listener, pattern)
	}
	return `{"analyzer":{"provider":"stub","model":"m"},"audit":{"file":"-"},` + top +
		`"listeners":[{"name":"api","protocol":"` + l.protocol + `","listen":"127.0.0.1:0",` +
		`"upstream":"` + l.upstream + `",` + l.codec + listener + `}]}`
}

func (s patternSite) runsOn(l patternLane) bool { return !s.httpOnly || l.protocol == "http" }

// A pattern that can never match is refused at every point a config passes:
// where the sidecar loads its file (boot and -validate read it the same
// way), where the control plane checks a document it is about to store, and
// on a heartbeat, which keeps the running config. The same document with a
// pattern that can match loads.
func TestAResourcePatternThatMatchesNothingIsRefused(t *testing.T) {
	for _, lane := range patternLanes {
		for _, site := range patternSites {
			if !site.runsOn(lane) {
				continue
			}
			name := lane.protocol + "/" + site.name
			bad := lane.doc(site, lane.bad)
			want := fmt.Sprintf(site.want, lane.bad)

			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: LoadConfig = %v, want it to contain %q", name, err, want)
			}
			if err := CheckConfigBytes([]byte(bad)); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: CheckConfigBytes = %v, want it to contain %q", name, err, want)
			}
			rl, buf := testReloader(t, lane.doc(patternSite{}, ""))
			if got := applyWith(rl, buf, bad); got != reloadRefused {
				t.Errorf("%s: reload outcome = %v, want refused; log:\n%s", name, got, buf)
			} else if !strings.Contains(rl.lastReason, want) || rl.gen != 0 {
				t.Errorf("%s: reload reason = %q at generation %d, want %q and the running config kept",
					name, rl.lastReason, rl.gen, want)
			}

			good := lane.doc(site, lane.good)
			if _, err := LoadConfigBytes([]byte(good)); err != nil {
				t.Errorf("%s: LoadConfigBytes(%s) = %v, want it loaded", name, lane.good, err)
			}
			if err := CheckConfigBytes([]byte(good)); err != nil {
				t.Errorf("%s: CheckConfigBytes(%s) = %v, want it accepted", name, lane.good, err)
			}
		}
	}
}

// Every pattern that can match a request still loads at every site: the
// codec's normalized file names, the dot segments a rule matches in the
// unresolved resource, escapes and a query string that loaded before.
func TestAResourcePatternThatCanMatchStillLoads(t *testing.T) {
	for _, lane := range patternLanes {
		for _, site := range patternSites {
			if !site.runsOn(lane) {
				continue
			}
			for _, p := range []string{
				"/", "/**", "/api/*/users", "/api/v1/**", "/api/v1/namespaces/*/secrets/**",
				"/reports/*.pdf", "/files/my%20report", "/api/users?limit=10", "/admin/../public",
			} {
				doc := lane.doc(site, p)
				if _, err := LoadConfigBytes([]byte(doc)); err != nil {
					t.Errorf("%s/%s: LoadConfigBytes(%s) = %v", lane.protocol, site.name, p, err)
				}
				if err := CheckConfigBytes([]byte(doc)); err != nil {
					t.Errorf("%s/%s: CheckConfigBytes(%s) = %v", lane.protocol, site.name, p, err)
				}
			}
		}
	}
}

// The control plane never builds a lane, so a pii rule must not hide the
// rule beside it: the rules are compiled as if a scanner were attached.
func TestAPIIRuleDoesNotHideAnInertPatternFromThePlane(t *testing.T) {
	doc := patternLanes[1].doc(patternSite{listener: `"guardrails":{"rules":[` +
		`{"name":"no-cpf","type":"pii","entities":["BR_CPF"]},` +
		`{"name":"deny-upload","type":"http_resource","resources":["%[1]s"]}]}`}, "/api/v1*/users")
	want := `deny-upload: resource "/api/v1*/users" matches no request as written`
	if err := CheckConfigBytes([]byte(doc)); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("CheckConfigBytes = %v, want it to contain %q", err, want)
	}
}

// The control plane saves an analyzer rule through the block export. The
// match_ignore resources are checked there too.
func TestTheAnalyzerBlockExportRefusesAnInertPattern(t *testing.T) {
	for _, lane := range patternLanes {
		for site, la := range map[string]*LaneAnalyzerConfig{
			"trigger.resources": {Trigger: &policy.AITrigger{Resources: []string{lane.bad}}},
			"trigger.any[0].resources": {Trigger: &policy.AITrigger{
				Any: []policy.AITriggerItem{{Resources: []string{lane.bad}}}}},
			"trigger.exclude[0].resources": {Trigger: &policy.AITrigger{
				Resources: []string{"/ok/*"},
				Exclude:   []policy.AITriggerItem{{Resources: []string{lane.bad}}}}},
		} {
			la.HighRisk = "block"
			want := fmt.Sprintf(`%s %q matches no request as written`, site, lane.bad)
			if got := strings.Join(ValidateLaneAnalyzerBlock(la, "rule"), "; "); !strings.Contains(got, want) {
				t.Errorf("%s %s: ValidateLaneAnalyzerBlock = %q, want %q", lane.protocol, site, got, want)
			}
		}
	}

	la := &LaneAnalyzerConfig{
		HighRisk: "require_review", ApprovalRule: "r",
		MatchIgnore: []analyzer.MatchIgnore{{
			Resources: []string{"/api/v1*/users"},
			Fields:    [][]string{{"metadata", "annotations"}},
		}},
	}
	want := `match_ignore[0] resources[0] "/api/v1*/users" matches no request as written`
	if got := strings.Join(ValidateLaneAnalyzerBlock(la, "rule"), "; "); !strings.Contains(got, want) {
		t.Errorf("match_ignore: ValidateLaneAnalyzerBlock = %q, want %q", got, want)
	}
}
