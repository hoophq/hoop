// Package sidecar assembles the hoop-inspect relay from a JSON config: audit
// sinks, policy evaluators, masking rules and one proxy.Server per listener.
//
// It sits between a client and a database or API, decodes the wire protocol,
// evaluates each statement against policy, records an audit trail naming the
// human who ran it, and masks sensitive values on the way back.
//
// # Deployment topology
//
// Run it behind something that already owns the network path and identity,
// typically an Envoy sidecar. Envoy terminates TLS, authenticates the user
// and forwards to hoop-inspect over loopback or a unix socket; hoop-inspect
// owns the payload. It routes nothing and balances nothing: one listener,
// one upstream, one protocol per endpoint.
//
// # Package here, binary in cmd
//
// The binary lives in the nested module cmd, because that is where the
// optional plugins are linked -- PII detection and the YAML config front end
// -- and a main in the root module could not import them without dragging
// their dependencies into the root's go.mod. Keeping the assembly here leaves
// the relay in the root module: the binary is a shell that supplies a
// detector and a loader, then calls Main. A caller embedding this library can
// call Run directly and skip the binary.
package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	_ "github.com/hoophq/hoop/sidecar/codec/all"
	"github.com/hoophq/hoop/sidecar/gate"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/license"
	"github.com/hoophq/hoop/sidecar/policy"
	"github.com/hoophq/hoop/sidecar/proxy"
	"github.com/hoophq/hoop/sidecar/session"
	"github.com/hoophq/hoop/sidecar/store"
)

// Version is the library release, reported by the admin /stats endpoint and
// the -version flag. Main overwrites it with the version its binary was
// stamped with; a caller embedding Run keeps this unless it assigns its own.
var Version = "0.1.0"

// Loader reads and validates a config file. It lets a build accept YAML
// without the root module linking a YAML parser: pass
// github.com/hoophq/hoop/sidecar/config/yaml's Load, or nil for JSON only.
type Loader func(path string) (*Config, error)

// PluginBuilder constructs the detection plugin from a config's "pii"
// section, the raw JSON of Config.PII.
//
// It is a function rather than a ready-made Plugin because the detector is
// built FROM the config: taking one already built would force every caller to
// read and parse the config file a second time before handing it over. Return
// a nil Plugin for an absent section. Pass nil to disable detection outright.
type PluginBuilder func(rawPII json.RawMessage) (Plugin, error)

// Option adjusts what Setup learns outside the config file.
//
// Variadic because the license arrived as a fourth PARAMETER once, and that
// broke every embedder at compile time. This module's README tells people to
// call Setup, so its signature is a contract: a new startup fact becomes a
// new Option, never a new argument and never a second Setup.
type Option func(*setupOptions)

type setupOptions struct {
	licenseFlag string
}

// WithLicense supplies a license from the command line, which outranks
// HOOP_LICENSE and the config file's `license` key.
//
// Only a caller with such a flag needs it. Setup reads the other two sources
// on its own, so an embedder that mounts a license file and names it in the
// config gets it from a plain three-argument call.
func WithLicense(ref string) Option {
	return func(o *setupOptions) { o.licenseFlag = ref }
}

// Setup loads a config file, resolves the license and builds the detection
// plugin.
//
// Three parameters, exactly as before licensing existed. This module's README
// tells embedders to call it, so the signature is a contract: adding the
// license as a fourth argument broke every one of them, and a variadic would
// still break a caller holding it in a typed variable. SetupWith is the same
// function with options.
func Setup(path string, load Loader, build PluginBuilder) (*Config, Plugin, error) {
	return SetupWith(path, load, build)
}

// SetupWith is Setup plus the facts an entry point learned outside the config
// file. A new startup fact becomes a new Option, never another parameter and
// never a third Setup.
//
// An UNREADABLE license stops startup and an expired one does not, since
// killing a data-path proxy over billing is an outage.
func SetupWith(path string, load Loader, build PluginBuilder, opts ...Option) (*Config, Plugin, error) {
	var o setupOptions
	for _, apply := range opts {
		apply(&o)
	}
	if load == nil {
		load = LoadConfig
	}
	cfg, err := load(path)
	if err != nil {
		return nil, nil, err
	}
	cfg.lic = ResolveLicense(o.licenseFlag, cfg.License)
	if cfg.lic.State() == license.StateInvalid {
		return nil, nil, cfg.lic.Err
	}
	if build == nil {
		return cfg, nil, nil
	}
	det, err := build(cfg.PII)
	if err != nil {
		return nil, nil, err
	}
	return cfg, det, nil
}

// ResolveLicense picks the license a process runs under, highest precedence
// first: the command line, then HOOP_LICENSE, then the config file's
// `license` key. Licensing a fleet must not mean editing every file in it.
// The control plane goes above all three once it sends one on connection.
func ResolveLicense(flagValue, fileValue string) license.Status {
	return license.Resolve(
		license.Ref{Value: flagValue, Source: "the license flag"},
		license.Ref{Value: os.Getenv(license.EnvVar), Source: license.EnvVar},
		license.Ref{Value: fileValue, Source: `the "license" config key`},
	)
}

// ErrUsage marks a command-line misuse: a missing or unparseable flag, as
// opposed to a bad config or a failed start. Main wraps it so a caller can
// exit 2 for misuse and 1 for everything else, the convention every other
// CLI on the box follows.
var ErrUsage = errors.New("usage")

// Main is the command-line entry point.
//
// version is stamped by the caller's -ldflags. load is the optional config
// reader; nil means JSON only. build is the optional detection plugin
// constructor; nil disables masking and makes any pii policy rule a config
// error.
//
// Main never exits the process: it returns the error and leaves the exit
// code and the error format to the main that called it. Report it and exit
// 2 when it matches ErrUsage, 1 otherwise.
//
// Usage:
//
//	hoop-inspect -config /etc/hoop-inspect/config.yaml
//	hoop-inspect -config config.yaml -license /etc/hoop-inspect/license.json
//	hoop-inspect -validate -config config.yaml   # check and exit
//	hoop-inspect -version
func Main(version string, load Loader, build PluginBuilder) error {
	Version = version

	syntax := "JSON"
	if load != nil {
		syntax = "YAML or JSON"
	}

	// A local FlagSet rather than flag.CommandLine: the global one is
	// ExitOnError, so a typo in an argument would kill the process from
	// inside this package before any of the code below ran.
	fs := flag.NewFlagSet("hoop-inspect", flag.ContinueOnError)
	var (
		configPath = fs.String("config", "", "path to the config file ("+syntax+")")
		licenseRef = fs.String("license", "", "path to the license file, or the license "+
			"document itself; overrides "+license.EnvVar+" and the config file's "+
			`"license" key`)
		validate = fs.Bool("validate", false, "validate the config and exit")
		strict   = fs.Bool("strict", false, "treat a deprecated config field as an error")
		showVer  = fs.Bool("version", false, "print the version and exit")
	)
	if err := fs.Parse(os.Args[1:]); err != nil {
		// -h is a request, not a mistake. ContinueOnError has already
		// printed the usage for both cases.
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}

	if *showVer {
		fmt.Println("hoop-inspect", version)
		return nil
	}
	if *configPath == "" {
		fs.Usage()
		return fmt.Errorf("%w: -config is required", ErrUsage)
	}

	cfg, det, err := SetupWith(*configPath, load, build, WithLicense(*licenseRef))
	if err != nil {
		return err
	}
	ReportDeprecations(os.Stderr, cfg.Deprecations)
	if *strict && len(cfg.Deprecations) > 0 {
		return fmt.Errorf("%d deprecated config field(s) in use and -strict is set",
			len(cfg.Deprecations))
	}

	if *validate {
		lanes, err := Validate(cfg, det)
		if err != nil {
			return err
		}
		return PrintLanes(os.Stdout, cfg.lic, lanes)
	}

	return Run(cfg, det)
}

// ReportDeprecations writes each deprecation notice to w, one per line.
//
// It goes to STDERR at every call site. -validate writes a report to stdout
// that an operator may parse, and a warning must not land in it. Same rule the
// CLI's rename notice follows.
//
// The write errors are DROPPED, and that is the whole difference from
// PrintLanes. A warning nobody could deliver must not turn a good config into
// a non-zero exit: the config is still valid, and failing the run would
// punish an operator for a broken stderr rather than for anything they wrote.
func ReportDeprecations(w io.Writer, notes []string) {
	for _, n := range notes {
		fmt.Fprintln(w, "warn: deprecated config:", n)
	}
	if len(notes) > 0 {
		fmt.Fprintln(w, "warn: these fields keep working for now and are removed in a "+
			"future release. See docs/adr/0011-sidecar-config-schema.md")
	}
}

// PrintLanes renders what -validate concluded: the license, the caps it
// leaves in force, one line per lane and the notes the resolved stack
// produced. The license goes to stdout, since an expired one is the likeliest
// reason a config stops loading and stdout is where "config OK" goes.
//
// It returns the write error, and both entry points exit non-zero on it. This
// report is what a pipeline reads to decide whether a config ships, so a run
// that could not deliver it must not also claim success: `hoop-inspect
// -validate > report.txt` on a full disk has produced no report, and an exit
// code of zero would say it produced a clean one.
//
// The body renders into a strings.Builder first. Builder.Write never fails,
// which is what makes the fmt calls below safe to ignore, and it leaves one
// write to check instead of one per line.
func PrintLanes(w io.Writer, lic license.Status, lanes []LaneInfo) error {
	var b strings.Builder
	fmt.Fprintln(&b, "config OK:", len(lanes), "listener(s)")
	fmt.Fprintf(&b, "  %s\n", lic.Line())
	fmt.Fprintf(&b, "  %s\n", LimitsSummary(lic))
	for _, ln := range lanes {
		fmt.Fprintf(&b, "  %-16s %-9s %s\n", ln.Name, ln.Protocol, ln.Summary())
		for _, n := range ln.Notes {
			fmt.Fprintf(&b, "  %-16s %-9s   note: %s\n", "", "", n)
		}
	}
	// The length check is not paranoia: io.WriteString hands back whatever
	// the Writer returned, so a Writer that reports a short count with a nil
	// error truncates the report and nothing notices. Half a report is worse
	// than none, because the half that arrived looks whole.
	out := b.String()
	n, err := io.WriteString(w, out)
	if err == nil && n != len(out) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return fmt.Errorf("writing the validation report: %w", err)
	}
	return nil
}

// LaneInfo is one resolved listener, as Validate reports it.
//
// Reporting the RESOLVED stack is the point: the config file does not show
// what a lane inherited, so a caller checking a config needs the merge
// result rather than the fields as written.
type LaneInfo struct {
	Name      string
	Protocol  string
	Enforcing bool
	Rules     int
	OPA       bool
	Masking   bool

	// Observing is true when the lane evaluates every rule and denies
	// nothing. Distinct from Enforcing being false, which now means the lane
	// resolved no rules at all: a dry run runs its rules, and an operator
	// reading this has to be able to tell the two apart.
	Observing bool

	// Notes are the facts about the resolved stack that no config file
	// shows. See lane.notes.
	Notes []string

	// Analyzed counts the ai_analysis rules on this lane. They are
	// reported apart from Rules because they behave differently in the way
	// that matters to whoever is reading a validate output: they leave the
	// process, they cost money, and they can be slow.
	Analyzed int
}

// Summary renders a LaneInfo as the one-line mode description the -validate
// output and the CLI both print.
func (l LaneInfo) Summary() string {
	var mode string
	switch {
	case l.Observing:
		mode = fmt.Sprintf("observing %d rule(s), denying none", l.Rules)
	case l.Enforcing:
		mode = fmt.Sprintf("enforcing %d rule(s)", l.Rules)
	default:
		mode = "no rules to enforce"
	}
	if l.OPA {
		mode += " + opa"
	}
	if l.Masking {
		mode += " + masking"
	}
	if l.Analyzed > 0 {
		mode += fmt.Sprintf(" + %d ai rule(s)", l.Analyzed)
	}
	return mode
}

// Validate builds every lane and reports what each one resolved to, without
// binding a port.
//
// Building the lanes covers most of what can go wrong in a config, and a
// check that skips it is one you stop trusting. With a detector attached it
// also proves the entity names resolve.
func Validate(cfg *Config, det Plugin) ([]LaneInfo, error) {
	if err := checkPIIPlugin(cfg, det); err != nil {
		return nil, err
	}
	ac, err := setupAnalyzer(cfg, det)
	if err != nil {
		return nil, err
	}
	// Prove the credential before reporting the config OK. Without this the
	// first sign of a bad key, a clock skew or a missing IAM binding is a
	// denied statement in production, which is the worst moment to learn it.
	if err := verifyAnalyzer(ac); err != nil {
		return nil, err
	}
	lanes, err := buildLanes(cfg, det, ac)
	if err != nil {
		return nil, err
	}
	out := make([]LaneInfo, 0, len(lanes))
	validationLog := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, ln := range lanes {
		notes := append([]string(nil), ln.notes...)
		if isGRPC(ln.cfg) {
			srv, err := buildGRPCServer(ln, cfg.Audit, nil, validationLog)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", ln.name, err)
			}
			notes = append(notes, srv.Notes()...)
			_ = srv.Close()
		}
		out = append(out, LaneInfo{
			Name:      ln.name,
			Protocol:  ln.cfg.Protocol,
			Enforcing: ln.policy != nil && !ln.observing,
			Observing: ln.observing,
			Rules:     len(ln.rules),
			OPA:       ln.opaURL != "",
			Masking:   ln.masker != nil,
			Analyzed:  len(ln.analyzed),
			Notes:     notes,
		})
	}
	return out, nil
}

// checkPIIPlugin rejects a "pii" section in a build with no detector.
//
// Dropping it silently would leave an operator believing their national-ID
// masking is on when the binary cannot do it.
func checkPIIPlugin(cfg *Config, det Plugin) error {
	if len(cfg.PII) > 0 && det == nil {
		return errors.New("config has a \"pii\" section but this build has no PII " +
			"detector; build github.com/hoophq/hoop/sidecar/cmd, or pass a detector, " +
			"or remove the section")
	}
	return nil
}

// reportLicense logs the state the process starts in, at the level it
// deserves. Expired warns: the process keeps serving under the free-tier
// caps and the operator has to see why a config that loaded last month now
// refuses a rule. Missing is information. Invalid never reaches here.
func reportLicense(log *slog.Logger, lic license.Status) {
	if lic.State() == license.StateExpired {
		log.Warn(lic.Line())
		return
	}
	log.Info(lic.Line())
}

// How often the run loop re-reads the clock against the license term, and how
// long before the term ends the log starts asking for a renewal.
//
// Polling beats one long timer: a timer set for a year misses an NTP
// correction and a host that slept through the expiry, and the cost here is
// one comparison a minute.
const (
	licenseCheckEvery = time.Minute
	licenseNotice     = 14 * 24 * time.Hour
)

// watchLicense stops the relay when the license term ends, and closes the
// returned channel to say so.
//
// Lanes are built once and hold their rules for the process lifetime, so the
// caps cannot be re-applied to a running relay. Dropping rules to fit the
// free tier would be worse than the problem it solves: removing a guardrail
// lets through statements it was refusing, and removing a mask rule leaks the
// values it was hiding. A billing event must never widen what a proxy allows.
//
// So the transition is a controlled stop. Run drains, flushes the audit trail
// and returns an error, the supervisor restarts, and buildLanes then refuses
// the config by name until somebody renews or removes rules. Callers start
// this only for a config that exceeds the free tier, because a process
// already inside the caps has nothing to take away.
func watchLicense(ctx context.Context, lic license.Status, every time.Duration, log *slog.Logger) <-chan struct{} {
	expired := make(chan struct{})
	go func() {
		tick := time.NewTicker(every)
		defer tick.Stop()
		notified := -1
		for {
			if lic.StateAt(time.Now().UTC()) == license.StateExpired {
				log.Warn(lic.Line())
				close(expired)
				return
			}
			notified = noticeLicenseExpiry(lic, notified, log)
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return expired
}

// noticeLicenseExpiry warns once a day through the last fortnight of a term,
// and returns the day count it last warned about.
//
// The stop at expiry is abrupt by design, so the notice is what keeps it from
// being a surprise. An operator who reads one line a day for two weeks and
// still lets the term lapse has made a decision.
func noticeLicenseExpiry(lic license.Status, lastNotified int, log *slog.Logger) int {
	left := time.Until(lic.ExpiresAt())
	if left <= 0 || left > licenseNotice {
		return lastNotified
	}
	days := int(left.Hours() / 24)
	if days == lastNotified {
		return lastNotified
	}
	log.Warn("license expires soon, and this config needs it: the relay stops when the "+
		"term ends",
		"days_left", days,
		"expires", lic.ExpiresAt().Format(time.RFC3339),
		"renew", license.Support)
	return days
}

// Run starts the sidecar and blocks until the process is signalled.
//
// det is the optional detection plugin. Passing nil disables masking and
// rejects any pii policy rule. Call Run to embed the relay in your own
// binary; the shipped one goes through Main.
//
// The license comes from the Config, where Setup left it. A Config assembled
// in Go carries the zero Status, so an embedder who never calls UseLicense
// gets the caps rather than a bypass.
func Run(cfg *Config, det Plugin) error {
	if err := checkPIIPlugin(cfg, det); err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)
	reportLicense(log, cfg.lic)
	limit := capsFor(cfg.lic)
	log.Info("rule limits",
		"guardrail_rules", capText(limit.guardrails),
		"mask_rules", capText(limit.mask))

	ac, err := buildAudit(cfg.Audit)
	if err != nil {
		return err
	}
	auditSink := ac.sink
	defer func() {
		// Close flushes buffered events, so shutdown does not drop the tail of
		// the audit trail.
		if cerr := auditSink.Close(); cerr != nil {
			log.Error("audit sink close failed", "error", cerr)
		}
	}()

	if det != nil {
		log.Info("detection plugin attached")
	}

	analyzerDeps, err := setupAnalyzer(cfg, det)
	if err != nil {
		return err
	}
	if err := verifyAnalyzer(analyzerDeps); err != nil {
		return err
	}
	if analyzerDeps != nil {
		log.Info("risk analyzer attached",
			"provider", cfg.Analyzer.Provider,
			"model", cfg.Analyzer.Model,
			"send", sendModeOrDefault(cfg.Analyzer.Send),
			"fail_open", cfg.Analyzer.failOpen())
	}

	lanes, err := buildLanes(cfg, det, analyzerDeps)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(),
		os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Only a config the free tier would refuse is worth watching. One inside
	// the caps keeps serving when its term ends, because expiry takes
	// nothing away from it.
	var licenseExpired <-chan struct{}
	if cfg.dependsOnLicense() {
		licenseExpired = watchLicense(ctx, cfg.lic, licenseCheckEvery, log)
	}

	// Two server kinds, one loop of lane facts. Relay lanes run
	// proxy.Server; grpc lanes run the transport the plugin registered
	// (ADR-0013). The stats zip in serveAdmin pairs servers with
	// relayNames, so the two slices must stay in lockstep.
	servers := make([]*proxy.Server, 0, len(lanes))
	relayNames := make([]string, 0, len(lanes))
	var grpcServers []GRPCServer
	var grpcNames []string
	for _, ln := range lanes {
		if isGRPC(ln.cfg) {
			gsrv, serr := buildGRPCServer(ln, cfg.Audit, auditSink, log)
			if serr != nil {
				return serr
			}
			grpcServers = append(grpcServers, gsrv)
			grpcNames = append(grpcNames, ln.name)
		} else {
			srv, serr := buildServer(ln, cfg.Audit, auditSink, log)
			if serr != nil {
				return serr
			}
			servers = append(servers, srv)
			relayNames = append(relayNames, ln.name)
		}

		// One line per lane naming what it enforces. The config file does not
		// show what a lane inherited, so this logs the RESOLVED stack: it turns
		// "why did this not deny" into a minute of reading instead of an
		// afternoon.
		log.Info("lane ready",
			"listener", ln.name,
			"protocol", ln.cfg.Protocol,
			"upstream", ln.cfg.Upstream,
			"enforcing", ln.policy != nil && !ln.observing,
			"observing", ln.observing,
			"rules", len(ln.rules),
			"opa", ln.opaURL,
			"masking", ln.masker != nil)
		for _, n := range ln.notes {
			log.Warn(n, "listener", ln.name)
		}
		if ln.policy == nil {
			// Not a misconfiguration on its own: a lane may exist to audit
			// and mask. It is worth one line, because a lane that resolved
			// no rules looks identical to one whose rules failed to reach it.
			log.Warn("lane resolved no rules and consults no policy; nothing will be denied",
				"listener", ln.name,
				"hint", "add guardrails.rules at the top level or on this listener")
		}
	}

	if cfg.Admin.Listen != "" {
		go serveAdmin(ctx, cfg.Admin.Listen, servers, relayNames, grpcServers, grpcNames,
			lanes, ac, cfg.Analyzer, cfg.lic, log)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(servers)+len(grpcServers))
	for i, srv := range servers {
		wg.Add(1)
		go func(s *proxy.Server, name string) {
			defer wg.Done()
			if serr := s.Serve(ctx); serr != nil {
				log.Error("listener failed", "listener", name, "error", serr)
				errCh <- serr
			}
		}(srv, relayNames[i])
	}
	for i, srv := range grpcServers {
		wg.Add(1)
		go func(s GRPCServer, name string) {
			defer wg.Done()
			if serr := s.Serve(ctx); serr != nil {
				log.Error("listener failed", "listener", name, "error", serr)
				errCh <- serr
			}
		}(srv, grpcNames[i])
	}
	var (
		stoppedByLicense bool
		listenerErr      error
	)
	select {
	case <-ctx.Done():
		log.Info("shutting down")
	case listenerErr = <-errCh:
		log.Info("shutting down after listener failure")
		cancel()
	case <-licenseExpired:
		stoppedByLicense = true
		log.Warn("stopping: the license term ended and this config needs more rules than "+
			"the free tier allows",
			"free_tier", limitsText(license.Status{}),
			"renew", license.Support)
		cancel()
	}
	for _, srv := range servers {
		_ = srv.Close()
	}
	for _, srv := range grpcServers {
		_ = srv.Close()
	}
	wg.Wait()
	close(errCh)

	// Report the first listener failure: a shutdown request must not hide a
	// bind error on one endpoint.
	for e := range errCh {
		if listenerErr == nil && e != nil {
			listenerErr = e
		}
	}
	if listenerErr != nil {
		return listenerErr
	}
	if stoppedByLicense {
		// A non-nil error exits non-zero, so a supervisor restarts and
		// buildLanes refuses the config by name. That loop is the point:
		// the operator renews or removes rules, and nothing in between
		// keeps serving licensed capacity for free.
		return fmt.Errorf("the license term ended and the free tier allows %s. "+
			"Renew at %s, or reduce the config and restart", limitsText(license.Status{}),
			license.Support)
	}
	return nil
}

// displayName is the lane's operator-facing name: what logs, audit rows,
// validation errors and input.context.connection all key on.
//
// normalize folds the deprecated `connection` field onto Name before anything
// calls this, so there is one name per lane rather than two that can disagree.
func (l ListenerConfig) displayName(i int) string {
	if l.Name != "" {
		return l.Name
	}
	return fmt.Sprintf("listener[%d]", i)
}

// lane is one listener's fully resolved enforcement stack: the listener
// itself plus the evaluator and masker built from its merged config.
//
// It exists so the merge happens once, at startup, in one place. Resolving
// per connection would put slice concatenation on the accept path and let two
// lanes disagree about what they inherited.
type lane struct {
	cfg    ListenerConfig
	name   string
	policy policy.Evaluator
	masker gate.Masker

	// codecFactory overrides the registry for this lane. Nil means the
	// registry default, which is every lane that did not configure capture.
	codecFactory func() inspect.Codec

	// rules and opaURL are the resolved facts the startup log and the
	// /config endpoint report. They sit alongside the built evaluator because
	// a policy.Chain cannot report what went into it.
	rules  []string
	opaURL string

	// analyzed names the ai_analysis rules on this lane, reported the same
	// way and for the same reason: the Chain cannot say what is in it, and
	// an operator needs to see that a lane is sending statements to a model.
	analyzed []string

	// captureBody reports whether this lane's codec exposes request bodies,
	// which decides whether HTTP analysis has anything to read.
	captureBody bool

	// observing is true when the lane evaluates everything and denies
	// nothing. Reported apart from policy != nil because a dry-run lane HAS
	// an evaluator: it is the one case where rules run and no denial can
	// reach the client.
	observing bool

	// notes are per-lane facts an operator has to know at startup and that
	// no config file shows, because they are consequences of the resolved
	// stack rather than of anything written down. A rule that defers on a
	// lane with no OPA is the case this exists for.
	notes []string
}

// buildLanes resolves and builds every listener's stack.
//
// Exhaustive rather than fail-fast, matching Validate: a config with three
// broken lanes reports three problems in one run, so you do not fix a fleet
// config one error per restart.
func buildLanes(cfg *Config, det Plugin, ac *analyzerDeps) ([]lane, error) {
	out := make([]lane, 0, len(cfg.Listeners))
	var problems []string

	// The one place the caps are enforced. (*Config).Validate cannot: it
	// runs inside LoadConfigBytes, before Setup resolves the license flag
	// or HOOP_LICENSE, so it would refuse a config for a limit its license
	// lifts. Every caller that runs or validates a sidecar reaches this.
	problems = append(problems, cfg.checkLimits(cfg.lic)...)

	for i, lc := range cfg.Listeners {
		name := lc.displayName(i)
		gc, opa, mc := cfg.resolve(lc)

		if errs := checkPIIEntities(gc.Rules, det); len(errs) > 0 {
			for _, e := range errs {
				problems = append(problems, name+": "+e)
			}
			continue
		}

		pol, err := buildPolicy(gc, opa, det, ac)
		if err != nil {
			problems = append(problems, name+": "+err.Error())
			continue
		}
		masker, err := buildMasker(mc, det, inspect.Protocol(lc.Protocol), lc.GRPC.hasDescriptors())
		if err != nil {
			problems = append(problems, name+": "+err.Error())
			continue
		}

		proto := inspect.Protocol(lc.Protocol)
		ln := lane{
			cfg:          lc,
			name:         name,
			policy:       pol,
			masker:       masker,
			codecFactory: httpCodecFactory(proto, lc.HTTP),
			captureBody:  lc.HTTP != nil && lc.HTTP.CaptureBody,
			observing:    gc.observing(),
		}
		// Reported whether or not the lane enforces. An observing lane runs
		// every one of these, and a reader of the startup log needs to see
		// which rules a dry run is exercising.
		ln.rules = make([]string, 0, len(gc.Rules))
		for _, r := range gc.Rules {
			if r.Type == policy.MatchAIAnalysis {
				ln.analyzed = append(ln.analyzed, r.Name)
				continue
			}
			ln.rules = append(ln.rules, r.Name)
		}
		if opa.enabled() {
			ln.opaURL = opa.URL
		}
		if gc.observing() {
			ln.notes = append(ln.notes,
				"observe mode: every rule is evaluated and nothing is denied. "+
					"Matches are recorded on the audit line as "+policy.AnnotationWouldDeny)
		}
		if !opa.enabled() && anyDeferred(gc.Rules) {
			ln.notes = append(ln.notes,
				"rule(s) defer to a decision this lane has no opa.url for, so a match "+
					"denies instead of reporting a finding")
		}
		out = append(out, ln)
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid config:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return out, nil
}

// checkPIIEntities rejects a pii rule naming an entity the detector was not
// configured to find.
//
// Without this the rule loads and evaluates cleanly: matchesPII intersects
// what the scanner reported with what the rule listed, so an entity the
// engine never looks for never appears, and the rule silently allows every
// statement it was written to deny. The operator sees a guardrail that is
// doing nothing.
func checkPIIEntities(rules []policy.Rule, det Plugin) []string {
	if det == nil {
		return nil // buildPolicy already refuses pii rules with no scanner
	}
	var known map[string]bool
	var problems []string

	for _, r := range rules {
		if r.Type != policy.MatchPII {
			continue
		}
		if known == nil {
			active := det.Entities()
			known = make(map[string]bool, len(active))
			for _, e := range active {
				known[e] = true
			}
		}
		for _, want := range r.Entities {
			if !known[want] {
				problems = append(problems, fmt.Sprintf(
					"rule %q names entity %q, which the detector is not configured to find; "+
						"drop it from pii.ignored, or fix the spelling, or the rule will "+
						"never match",
					r.Name, want))
			}
		}
	}
	return problems
}

// buildServer turns one resolved lane into a running-capable Server.
func buildServer(
	ln lane,
	ac AuditConfig,
	sink audit.Sink,
	log *slog.Logger,
) (*proxy.Server, error) {
	lc := ln.cfg
	upstreamTLS, err := lc.UpstreamTLS.BuildTLS()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ln.name, err)
	}
	if upstreamTLS != nil && upstreamTLS.InsecureSkipVerify {
		// Warn loudly. A proxy built to inspect sensitive traffic should not
		// quietly accept any upstream certificate.
		log.Warn("upstream certificate verification is DISABLED",
			"listener", ln.name, "upstream", lc.Upstream)
	}

	downstreamTLS, err := lc.DownstreamTLS.BuildDownstreamTLS()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ln.name, err)
	}
	if downstreamTLS != nil {
		log.Info("terminating the client's TLS on this lane",
			"listener", ln.name,
			"reason", "pgwire negotiates TLS in-band, so nothing in front can")
	}

	var identityFn func(net.Conn) session.Identity
	if lc.IdentityHeader != "" {
		identityFn = headerIdentity(lc.IdentityHeader)
	}

	return proxy.NewServer(proxy.Config{
		Listen:           lc.Listen,
		Network:          lc.Network,
		Upstream:         lc.Upstream,
		UpstreamTLS:      upstreamTLS,
		DownstreamTLS:    downstreamTLS,
		Protocol:         inspect.Protocol(lc.Protocol),
		Connection:       ln.name,
		Policy:           ln.policy,
		Audit:            sink,
		Masker:           ln.masker,
		FailOnAuditError: ac.failOnAuditError(),
		DenyWriter:       proxy.ProtocolDenyWriter{},
		IdentityFn:       identityFn,
		CodecFactory:     ln.codecFactory,
		IdleTimeout:      time.Duration(lc.IdleTimeoutSec) * time.Second,
		MaxConns:         lc.MaxConns,
		Logger:           log.With("listener", ln.name),
	})
}

// buildMasker asks the plugin to compile one lane's "mask" section.
//
// Two refusals rather than silent downgrades, because you discover both
// failures the same way: by finding an unmasked SSN in a screenshot.
//
//   - No plugin. Masking needs a detection engine and this package links
//     none, so a mask section without one cannot work.
//   - A protocol whose framing cannot survive substitution. The rules would
//     load, validate, and never fire.
//
// A non-empty rule list is the whole of the switch. The separate `enabled`
// flag it replaced used to skip these checks along with the masking, so a
// lane could carry rules for a protocol that cannot mask and still load
// clean.
//
// grpc takes neither of gate.MaskSupported's paths: the lane rewrites
// decoded fields and re-encodes the message itself, which is possible
// exactly when it holds a descriptor set (ADR-0013). grpcDescriptors is
// that fact; every other protocol ignores it.
//
// Validate reports both at startup; these checks cover a caller reaching Run
// without going through LoadConfig.
func buildMasker(mc MaskConfig, det Plugin, proto inspect.Protocol, hasGRPCDescriptors bool) (gate.Masker, error) {
	if !mc.hasRules() {
		return nil, nil
	}
	if det == nil {
		return nil, fmt.Errorf("mask.rules is set but this build has no detection plugin")
	}
	if proto == inspect.GRPC {
		if !hasGRPCDescriptors {
			return nil, fmt.Errorf(
				"mask.rules is set but this grpc lane has no grpc.descriptors; without a " +
					"descriptor set the lane cannot decode a message to rewrite it")
		}
	} else if !gate.MaskSupported(proto) {
		return nil, fmt.Errorf(
			"mask.rules is set but masking is not supported on %s; remove the rules from "+
				"this lane, or set mask: {rules: []} on it", proto)
	}
	return det.BuildMasker(mc.Rules)
}

func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	// JSON to stderr: audit events go to their own sink, so stderr carries
	// only operational output and a container platform can route the two
	// differently.
	h := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: lv})
	l := slog.New(h)
	slog.SetDefault(l)
	return l
}

// auditChain is what buildAudit assembles: the sink the gate writes to, plus
// the read-side handles the admin endpoint needs.
type auditChain struct {
	sink  audit.Sink
	mem   *audit.MemorySink
	query *store.MemoryStore
}

// buildAudit assembles the sink chain.
//
// Order matters: the durable JSONL sink is first so it records even if a
// later sink errors, and MultiSink attempts every sink regardless.
func buildAudit(cfg AuditConfig) (auditChain, error) {
	opts := audit.SinkOptions{
		RedactStatements:  cfg.RedactStatements,
		MaxStatementBytes: cfg.MaxStatementBytes,
	}

	var out auditChain
	var sinks []audit.Sink

	switch cfg.File {
	case "", "-":
		// No file configured: record to stdout rather than discard. A
		// deployment that wants no audit trail says so by pointing the file at
		// /dev/null.
		sinks = append(sinks, audit.NewJSONLSink(os.Stdout, opts))
	default:
		f, err := os.OpenFile(cfg.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
		if err != nil {
			return out, fmt.Errorf("open audit file: %w", err)
		}
		sinks = append(sinks, audit.NewJSONLSink(f, opts))
	}

	if cfg.MemoryBuffer > 0 {
		out.mem = audit.NewMemorySink(cfg.MemoryBuffer)
		sinks = append(sinks, out.mem)
	}

	// The query store is both a sink and the read side, so it indexes on the
	// same write that records the event. Two pipelines would drift.
	if cfg.QuerySessions > 0 {
		out.query = store.NewMemoryStore(cfg.QuerySessions)
		sinks = append(sinks, out.query)
	}

	out.sink = audit.NewMultiSink(sinks...)
	if cfg.AsyncQueueSize > 0 {
		out.sink = audit.NewAsyncSink(out.sink, cfg.AsyncQueueSize)
	}
	return out, nil
}

// serveAdmin exposes health and stats.
//
// It binds separately from the data path so a platform can scrape it without
// reaching the proxy, and vice versa.
func serveAdmin(
	ctx context.Context,
	addr string,
	servers []*proxy.Server,
	relayNames []string,
	grpcServers []GRPCServer,
	grpcNames []string,
	lanes []lane,
	ac auditChain,
	analyzerCfg *AnalyzerConfig,
	lic license.Status,
	log *slog.Logger,
) {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	mux.HandleFunc("GET /stats", func(w http.ResponseWriter, r *http.Request) {
		type stat struct {
			Name   string `json:"name"`
			Addr   string `json:"addr"`
			Active int64  `json:"active"`
			Total  int64  `json:"total"`
			Denied int64  `json:"denied"`
		}
		out := make([]stat, 0, len(servers)+len(grpcServers))
		for i, s := range servers {
			active, total, denied := s.Stats()
			a := ""
			if s.Addr() != nil {
				a = s.Addr().String()
			}
			// servers and relayNames are built in lockstep from the relay
			// listeners, so the index is the join. gRPC servers are appended
			// below from their own lockstep slices.
			out = append(out, stat{
				Name: relayNames[i], Addr: a,
				Active: active, Total: total, Denied: denied,
			})
		}
		for i, s := range grpcServers {
			active, total, denied := s.Stats()
			a := ""
			if s.Addr() != nil {
				a = s.Addr().String()
			}
			out = append(out, stat{
				Name: grpcNames[i], Addr: a,
				Active: active, Total: total, Denied: denied,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"version":   Version,
			"listeners": out,
		})
	})

	// The resolved enforcement stack, per lane.
	//
	// The config file does not show inheritance: a lane's rules are its own
	// plus whatever the top level contributed, and reading the file cannot
	// tell you the result. Debugging a missing denial starts with "which rules
	// is this lane running", and this endpoint answers it.
	//
	// Rule NAMES only. A rule's pattern_regex can encode business logic, and
	// this endpoint already sits beside a read interface to the audit trail.
	mux.HandleFunc("GET /config", func(w http.ResponseWriter, r *http.Request) {
		type laneView struct {
			Name      string   `json:"name"`
			Protocol  string   `json:"protocol"`
			Listen    string   `json:"listen"`
			Upstream  string   `json:"upstream"`
			Enforcing bool     `json:"enforcing"`
			Rules     []string `json:"rules"`
			OPA       string   `json:"opa,omitempty"`
			Masking   bool     `json:"masking"`
			MaskNote  string   `json:"mask_note,omitempty"`

			// Observing, Guardrails and OPAURL are the ADR-0011
			// vocabulary. They sit BESIDE the four fields above rather
			// than replacing them, so a control plane reading
			// "enforcing" keeps working while it migrates. The
			// response names the old set in deprecated_fields.
			Observing  bool     `json:"observing"`
			Guardrails string   `json:"guardrails"`
			OPAURL     string   `json:"opa_url,omitempty"`
			OPAGate    bool     `json:"opa_gate,omitempty"`
			Notes      []string `json:"notes,omitempty"`

			// AIRules names the ai_analysis rules and CaptureBody says
			// whether this lane's codec exposes request bodies. Both
			// are here because they answer "what leaves this process",
			// which is the question an operator has about an analyzer
			// and cannot answer from the config file alone.
			AIRules     []string `json:"ai_rules,omitempty"`
			CaptureBody bool     `json:"capture_body,omitempty"`
		}
		out := make([]laneView, 0, len(lanes))
		for _, ln := range lanes {
			mode := ModeEnforce
			if ln.observing {
				mode = ModeObserve
			}
			v := laneView{
				Name:        ln.name,
				Protocol:    ln.cfg.Protocol,
				Listen:      ln.cfg.Listen,
				Upstream:    ln.cfg.Upstream,
				Enforcing:   ln.policy != nil && !ln.observing,
				Rules:       ln.rules,
				OPA:         ln.opaURL,
				Masking:     ln.masker != nil,
				AIRules:     ln.analyzed,
				CaptureBody: ln.captureBody,
				Observing:   ln.observing,
				Guardrails:  mode,
				OPAURL:      ln.opaURL,
				Notes:       ln.notes,
			}
			if v.Rules == nil {
				v.Rules = []string{} // render [] rather than null
			}
			if !v.Masking && !gate.MaskSupported(inspect.Protocol(ln.cfg.Protocol)) {
				v.MaskNote = "masking is not supported on this protocol"
			}
			out = append(out, v)
		}
		limit := capsFor(lic)
		resp := map[string]any{
			"version": Version,
			"lanes":   out,
			// Named in the payload rather than only in the README, so a
			// control plane can warn its own developers without reading
			// prose. These keys still carry correct values.
			"deprecated_fields": []string{"enforcing", "rules", "opa", "masking"},
			// What this process refuses to exceed. An operator asking why a
			// second rule will not load gets the answer from the same
			// endpoint that tells them what did load, and a null says the
			// license lifted the cap rather than that the cap is zero.
			"limits": map[string]any{
				"guardrail_rules": capJSON(limit.guardrails),
				"mask_rules":      capJSON(limit.mask),
			},
			// Beside the limits because it is the reason for them. Never
			// the signature: an endpoint handing out a complete, reusable
			// license is a licensing hole with an HTTP interface.
			"license": lic.Report(),
		}
		// The analyzer view names the provider, the model and the HOST it
		// talks to — never the path, never a query string, and never the
		// credential, which this process holds as an unprintable Secret
		// and which the config only ever named by file path.
		if analyzerCfg != nil {
			resp["analyzer"] = map[string]any{
				"provider":      analyzerCfg.Provider,
				"model":         analyzerCfg.Model,
				"endpoint_host": analyzerCfg.endpointHost(),
				"send":          sendModeOrDefault(analyzerCfg.Send),
				"fail_open":     analyzerCfg.failOpen(),
				// Whether a custom prompt is in effect, never its text: a
				// prompt describes what a deployment considers risky, which
				// is business logic, and this endpoint sits beside a read
				// interface to the audit trail. Same rule as reporting rule
				// NAMES rather than their pattern_regex.
				"custom_prompt": analyzerCfg.Prompt != "",
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	if ac.mem != nil {
		// The recent-events endpoint is a debugging aid. The ring drops old
		// events, so a compliance reader gets an incomplete record.
		mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"events":  ac.mem.Events(),
				"dropped": ac.mem.Dropped(),
				"warning": "in-memory ring buffer; not a complete audit trail",
			})
		})
	}

	// The query API: the endpoints a UI renders. Mounted under /api so the
	// operational endpoints above (/healthz, /stats) stay stable and
	// separately scrapeable.
	//
	// It sits on the ADMIN listener by design. This is a read interface to
	// every statement every user ran; it belongs behind whatever already
	// decides who may see audit data, and the data-path ports must never
	// serve it.
	if ac.query != nil {
		api := store.NewAPI(ac.query)
		api.BasePath = "/api"
		mux.Handle("/api/", api)
		log.Info("query API mounted",
			"paths", "/api/sessions, /api/sessions/{id}, /api/sessions/{id}/events, /api/events, /api/stats")
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	log.Info("admin endpoint listening", "listen", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("admin endpoint failed", "error", err)
	}
}

// headerIdentity extracts the authenticated subject from an HTTP header on
// the first bytes of a connection.
//
// This peeks at the connection without consuming it, which a plain net.Conn
// cannot do, so only listeners that opt in via IdentityHeader get it. It is
// safe only when nothing but the authenticating proxy can reach the
// listener.
func headerIdentity(header string) func(net.Conn) session.Identity {
	return func(c net.Conn) session.Identity {
		// The proxy reads the stream itself; extracting the header here would
		// require buffering ahead of the relay. Instead of duplicating that
		// machinery, the gate resolves http identity from the inspected
		// request, and this function contributes only the peer address.
		//
		// It stays a named function so the config surface says where identity
		// comes from today.
		return session.Identity{PeerAddr: c.RemoteAddr().String()}
	}
}
