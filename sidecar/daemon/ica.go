package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hoophq/hoop/sidecar/audit"
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/session"
)

// An ica lane embeds the libhoop-ica engine and uses no relay and no codec.
// The engine terminates Citrix ICA/CGP sessions between a Workspace client
// (or Citrix Gateway) and a VDA, decodes the ThinWire video channel, runs
// OCR and NER on it, and masks sensitive pixels before it re-encodes the
// frame toward the client. An ica lane has no statement, so this package's
// gate, policy evaluation and masker have nothing to act on: the engine's
// fail-closed pipeline enforces, and the sidecar supplies admission policy,
// identity labels, audit and lifecycle.
//
// The engine lives in github.com/hoophq/libhoop-ica. The root module must
// not import it (its one dependency is libhoop), so this file defines the
// contract: the config an operator writes, what the daemon hands an engine,
// and what the daemon expects back. The nested sidecar/ica module registers
// the engine through RegisterICAEngine, as sidecar/mcp registers its server.
//
// `upstream:` selects single mode and names the VDA; `ica.transparent:`
// selects CIDR mode, where the VDA for each connection is the original
// destination TPROXY kept on the accepted socket (Linux, IP_TRANSPARENT,
// CAP_NET_ADMIN). Config validation refuses both together. After accept,
// both modes run the same engine path.

// isICA reports whether a listener is an ica lane. Like isSSH, it exists
// because ica registers no codec: code that would call inspect.New for a
// listener checks isICA first.
func isICA(lc ListenerConfig) bool {
	return inspect.Protocol(lc.Protocol) == inspect.ICA
}

// ICARuntimeConfig is the top-level `ica` block: the OCR/NER runtime that
// all ica lanes in this process share. Loading the NER model and spawning
// the OCR workers takes minutes and gigabytes, so the daemon runs one
// runtime per process. With no block, the engine's production profile
// supplies every path and setting.
//
// The profile lives in the engine (libhoop-ica/ica.DefaultRuntimeConfig),
// which is also the source of the agent's flag defaults, so all binaries
// share one profile. Options holds overrides only; the lane's Notes report
// the resolved values.
type ICARuntimeConfig struct {
	// TurboOCR native device assets; used only with options.ocr_backend: turboocr.
	OCRTurboLibrary  string `json:"ocr_turbo_library,omitempty" cap:"ica_gpu"`
	OCRTurboDetModel string `json:"ocr_turbo_det_model,omitempty" cap:"ica_gpu"`
	OCRTurboRecModel string `json:"ocr_turbo_rec_model,omitempty" cap:"ica_gpu"`
	OCRTurboRecDict  string `json:"ocr_turbo_rec_dict,omitempty" cap:"ica_gpu"`
	OCRTurboClsModel string `json:"ocr_turbo_cls_model,omitempty" cap:"ica_gpu"`
	// OCRWorker is the hoop-tesseract-worker binary. Default: the engine's
	// installed-release path (/opt/libhoop-ica/current/bin/hoop-tesseract-worker).
	OCRWorker string `json:"ocr_worker,omitempty"`
	// Tessdata is the Tesseract data directory. Default: /usr/share/tesseract-ocr/5/tessdata.
	Tessdata string `json:"tessdata,omitempty"`
	// NERModel is the offline KnightsAnalytics/distilbert-NER directory.
	// Default: /opt/libhoop-ica/current/models/KnightsAnalytics_distilbert-NER.
	NERModel string `json:"ner_model,omitempty"`
	// NERORTLibrary is libonnxruntime.so, read only by the ort backend.
	// Default: /opt/alcatraz/ort/releases/1.28.0/lib/libonnxruntime.so.
	NERORTLibrary string `json:"ner_ort_library,omitempty"`
	// MaxSessions bounds concurrent ICA sessions across every ica lane.
	// Default 32, 1..4096.
	MaxSessions int `json:"max_sessions,omitempty"`
	// ConnectTimeoutSec bounds the VDA dial. Default 10, 1..60.
	ConnectTimeoutSec int `json:"connect_timeout_sec,omitempty"`
	// FailureDiagnosticDir enables the engine's metadata-only failure files
	// (no pixels) in an existing private (0700) directory. Empty disables
	// them.
	FailureDiagnosticDir string `json:"failure_diagnostic_dir,omitempty"`
	// Options overrides individual profile settings. See ICAOptions.
	Options *ICAOptions `json:"options,omitempty"`
	// EDT bounds the retained EDT sessions of every lane that sets ica.edt.
	// With no block, the engine's defaults apply.
	EDT *ICAEDTRuntimeConfig `json:"edt,omitempty" cap:"ica_edt"`
}

// ICAEDTRuntimeConfig tunes experimental EDT. An EDT session survives a
// network change by suspending; retention is how long a suspended session
// waits for its client to reconnect, and the replay budget bounds the bytes
// queued for replay across all EDT sessions of a lane.
type ICAEDTRuntimeConfig struct {
	// RetentionSec is 1..600; 0 takes the engine's default (120).
	RetentionSec int `json:"retention_sec,omitempty"`
	// ReplayBudgetBytes is at most 8 GiB; 0 takes the engine's default
	// (512 MiB). The engine also refuses a budget below what max_sessions
	// needs, at start.
	ReplayBudgetBytes int64 `json:"replay_budget_bytes,omitempty"`
}

// The engine's bounds for ICAEDTRuntimeConfig (libhoop-ica/ica
// MaxEDTRetention and MaxEDTReplayBudgetBytes). This file cannot import the
// engine; sidecar/ica's tests compare the two.
const (
	icaMaxEDTRetentionSec      = 600
	icaMaxEDTReplayBudgetBytes = 8 << 30
)

// ICAOptions overrides the engine's production profile. Each field is a
// pointer, and nil keeps the profile's value. Field names are the agent's
// flag names with underscores, so an operator who knows
// -ocr-fresh-reuse-frames finds ocr_fresh_reuse_frames. ICAOptions omits the
// agent's UNSAFE and LAB ONLY modes; a lane that needs them runs the
// standalone agent.
//
// validate checks ranges here so the control plane refuses a typo without
// an engine. Cross-setting rules (adaptive_coalescing needs
// output_pipeline…) belong to the engine, and its validator checks them on
// the host.
type ICAOptions struct {
	// Backend selection keeps CPU defaults unchanged. TurboOCR relaxes only
	// implicit Tesseract settings; explicit incompatible overrides are refused.
	OCRBackend               *string  `json:"ocr_backend,omitempty" cap:"ica_gpu"`
	OCRTurboDevice           *int     `json:"ocr_turbo_device,omitempty" cap:"ica_gpu"`
	OCRTurboFramebufferBytes *uint64  `json:"ocr_turbo_framebuffer_bytes,omitempty" cap:"ica_gpu"`
	OCRTurboFullScreen       *bool    `json:"ocr_turbo_full_screen,omitempty" cap:"ica_gpu"`
	OCRTurboRegionalCache    *bool    `json:"ocr_turbo_regional_cache,omitempty" cap:"ica_gpu"`
	NERAccelerator           *string  `json:"ner_accelerator,omitempty" cap:"ica_gpu"`
	NERGPUDevice             *int     `json:"ner_gpu_device,omitempty" cap:"ica_gpu"`
	NEREntities              []string `json:"ner_entities,omitempty" cap:"ica_gpu"`
	OCRWorkers               *int     `json:"ocr_workers,omitempty"`
	OCRTiles                 *int     `json:"ocr_tiles,omitempty"`
	OCRThreadLimit           *int     `json:"ocr_thread_limit,omitempty"`
	OCRTimeoutSec            *int     `json:"ocr_timeout_sec,omitempty"`
	OCRMinConfidence         *float64 `json:"ocr_min_confidence,omitempty"`
	OCRBrowserOptimizations  *bool    `json:"ocr_browser_optimizations,omitempty"`
	OCRScreenOptimizations   *bool    `json:"ocr_screen_optimizations,omitempty"`
	OCRGrayPreparation       *bool    `json:"ocr_gray_preparation,omitempty"`
	OCRNoInvert              *bool    `json:"ocr_no_invert,omitempty"`
	OCRFreshReuseFrames      *int     `json:"ocr_fresh_reuse_frames,omitempty"`
	OCRLineBandScale         *bool    `json:"ocr_line_band_scale,omitempty"`
	OCRLineBandMinRows       *int     `json:"ocr_line_band_min_rows,omitempty"`
	NERBackend               *string  `json:"ner_backend,omitempty"`
	NERThreads               *int     `json:"ner_threads,omitempty"`
	NERParallel              *int     `json:"ner_parallel,omitempty"`
	PIIThreshold             *float64 `json:"pii_threshold,omitempty"`
	// PIIMatchesOnly replaces masks from fresh full-screen CPU OCR matches;
	// it disables historical uncertainty coverage, not protocol checks.
	PIIMatchesOnly *bool `json:"pii_matches_only,omitempty" cap:"ica_pii_matches_only"`
	// PIIEntities replaces the profile's structured entity set
	// (HEALTHCARE_IDENTIFIERS) when non-empty. NER runs in both cases.
	PIIEntities            []string `json:"pii_entities,omitempty"`
	VideoBatch             *bool    `json:"video_batch,omitempty"`
	OutputPipeline         *bool    `json:"output_pipeline,omitempty"`
	AdaptiveCoalescing     *bool    `json:"adaptive_coalescing,omitempty"`
	PresentationV2         *bool    `json:"presentation_v2,omitempty"`
	PresentationScreenCopy *bool    `json:"presentation_screen_copy,omitempty"`
	OutputLatestFrame      *bool    `json:"output_latest_frame,omitempty"`
}

func (r *ICARuntimeConfig) validate(onHost bool, listeners []ListenerConfig) []string {
	if r == nil {
		return nil
	}
	var problems []string
	where := "ica"
	if r.MaxSessions != 0 && (r.MaxSessions < 1 || r.MaxSessions > 4096) {
		problems = append(problems, where+": max_sessions must be 1..4096")
	}
	if r.ConnectTimeoutSec != 0 && (r.ConnectTimeoutSec < 1 || r.ConnectTimeoutSec > 60) {
		problems = append(problems, where+": connect_timeout_sec must be 1..60")
	}
	// validate checks an explicit path only on the host, where the engine
	// reads the file. It skips default paths: the image and the installer
	// lay those out, and a developer's -validate of a config that names no
	// path must not depend on them.
	if onHost {
		for key, path := range map[string]string{
			"ocr_worker": r.OCRWorker, "tessdata": r.Tessdata,
			"ner_model": r.NERModel, "ner_ort_library": r.NERORTLibrary,
			"failure_diagnostic_dir": r.FailureDiagnosticDir,
			"ocr_turbo_library":      r.OCRTurboLibrary, "ocr_turbo_det_model": r.OCRTurboDetModel,
			"ocr_turbo_rec_model": r.OCRTurboRecModel, "ocr_turbo_rec_dict": r.OCRTurboRecDict,
			"ocr_turbo_cls_model": r.OCRTurboClsModel,
		} {
			if path == "" {
				continue
			}
			if _, err := os.Stat(path); err != nil {
				problems = append(problems, fmt.Sprintf("%s.%s: %v", where, key, err))
			}
		}
	}
	problems = append(problems, r.Options.validate(where+".options")...)
	if e := r.EDT; e != nil {
		if e.RetentionSec != 0 && (e.RetentionSec < 1 || e.RetentionSec > icaMaxEDTRetentionSec) {
			problems = append(problems, fmt.Sprintf("%s.edt.retention_sec must be 1..%d", where, icaMaxEDTRetentionSec))
		}
		if e.ReplayBudgetBytes != 0 && (e.ReplayBudgetBytes < 1 || e.ReplayBudgetBytes > icaMaxEDTReplayBudgetBytes) {
			problems = append(problems, fmt.Sprintf("%s.edt.replay_budget_bytes must be 1..%d (8 GiB)", where, int64(icaMaxEDTReplayBudgetBytes)))
		}
		if !slices.ContainsFunc(listeners, func(l ListenerConfig) bool { return isICA(l) && l.ICA != nil && l.ICA.EDT }) {
			problems = append(problems, where+".edt is set but no ica listener sets edt: true; remove the block or turn EDT on for a lane")
		}
	}
	return problems
}

func (o *ICAOptions) validate(where string) []string {
	if o == nil {
		return nil
	}
	var problems []string
	rng := func(name string, v *int, lo, hi int) {
		if v != nil && (*v < lo || *v > hi) {
			problems = append(problems, fmt.Sprintf("%s.%s must be %d..%d", where, name, lo, hi))
		}
	}
	unit := func(name string, v *float64) {
		if v != nil && (*v < 0 || *v > 1) {
			problems = append(problems, fmt.Sprintf("%s.%s must be 0..1", where, name))
		}
	}
	rng("ocr_turbo_device", o.OCRTurboDevice, 0, 65535)
	rng("ner_gpu_device", o.NERGPUDevice, 0, 65535)
	if o.OCRBackend != nil && *o.OCRBackend != "tesseract" && *o.OCRBackend != "turboocr" {
		problems = append(problems, where+".ocr_backend must be tesseract or turboocr")
	}
	if o.NERAccelerator != nil && *o.NERAccelerator != "cpu" && *o.NERAccelerator != "cuda" {
		problems = append(problems, where+".ner_accelerator must be cpu or cuda")
	}
	for _, e := range o.NEREntities {
		if !slices.Contains([]string{"PERSON", "LOCATION", "ORGANIZATION", "NRP"}, e) {
			problems = append(problems, where+".ner_entities must contain PERSON, LOCATION, ORGANIZATION or NRP")
			break
		}
	}
	rng("ocr_workers", o.OCRWorkers, 1, 64)
	rng("ocr_tiles", o.OCRTiles, 1, 16)
	rng("ocr_thread_limit", o.OCRThreadLimit, 0, 16)
	rng("ocr_timeout_sec", o.OCRTimeoutSec, 1, 300)
	rng("ocr_fresh_reuse_frames", o.OCRFreshReuseFrames, 0, 1000)
	rng("ocr_line_band_min_rows", o.OCRLineBandMinRows, 0, 96)
	rng("ner_threads", o.NERThreads, 1, 16)
	rng("ner_parallel", o.NERParallel, 1, 16)
	unit("ocr_min_confidence", o.OCRMinConfidence)
	unit("pii_threshold", o.PIIThreshold)
	if o.NERBackend != nil && *o.NERBackend != "go" && *o.NERBackend != "ort" {
		problems = append(problems, where+".ner_backend must be go or ort")
	}
	for _, e := range o.PIIEntities {
		if strings.TrimSpace(e) == "" {
			problems = append(problems, where+".pii_entities has an empty entry")
			break
		}
	}
	return problems
}

// ICAConfig is the `ica` block on a listener: who the lane serves and how
// it finds the VDA. An ica lane requires it, and config validation refuses
// it on any other protocol.
type ICAConfig struct {
	// TenantID labels every session and audit row this lane produces. The
	// audit trail records it as the principal, because an ICA session
	// authenticates to the VDA and not to the sidecar.
	TenantID string `json:"tenant_id,omitempty" label:"Tenant" placeholder:"citrix-production" help:"Labels every session and audit row from this listener." ui:"required"`

	// Transparent selects CIDR mode: the VDA is the original destination of
	// each intercepted connection, and the listener needs Linux
	// IP_TRANSPARENT, CAP_NET_ADMIN and a TPROXY rule. With no block, the
	// lane runs in single mode and `upstream` names the VDA.
	Transparent *ICATransparentConfig `json:"transparent,omitempty" label:"Transparent (CIDR) mode" help:"Selects the VDA for each connection from its intercepted destination. Requires Linux, CAP_NET_ADMIN and a TPROXY rule." ui:"presence"`

	// SourceCIDRs, in single mode, are the networks allowed to connect: the
	// Citrix Gateway SNIP when the Gateway dials this lane as the VDA. Empty
	// admits any source, as the other sidecar lanes do. CIDR mode takes its
	// list from transparent.source_cidrs instead and refuses this one.
	SourceCIDRs []string `json:"source_cidrs,omitempty" cap:"ica_source_cidrs" label:"Source CIDRs" placeholder:"10.20.1.10/32" help:"Single mode: networks allowed to connect, such as the Citrix Gateway SNIP. Empty admits any source."`

	// Display pins the framebuffer geometry and ThinWire channel for every
	// session. With no block, the engine's defaults apply (1264x1040,
	// channel 9).
	Display *ICADisplayConfig `json:"display,omitempty" label:"Display" help:"Framebuffer geometry and ThinWire channel. Leave empty for the engine defaults."`

	// EDT also serves Citrix EDT (UDP) on the listener's address, beside
	// TCP: same port, same policy, same session bound. The engine marks EDT
	// experimental. A client that cannot reach the UDP port falls back to
	// TCP.
	EDT bool `json:"edt,omitempty" cap:"ica_edt" label:"EDT (experimental)" help:"Also accept Citrix EDT over UDP on the same address and port. Experimental; clients fall back to TCP."`
}

// ICATransparentConfig is CIDR mode's admission policy.
type ICATransparentConfig struct {
	// SourceCIDRs are the Gateway or Workspace client networks allowed to
	// connect. CIDR mode requires them, because a transparent listener sits
	// on-path for all traffic the router sends it.
	SourceCIDRs []string `json:"source_cidrs,omitempty" label:"Source CIDRs" placeholder:"10.10.1.10/32" help:"Networks allowed to connect: the Citrix Gateway SNIP or the Workspace client networks." ui:"required"`
	// UpstreamCIDRs are the VDA networks a connection may be destined to.
	UpstreamCIDRs []string `json:"upstream_cidrs,omitempty" label:"VDA CIDRs" placeholder:"10.77.0.0/16" help:"VDA networks a connection may be destined to." ui:"required"`
	// UpstreamPorts are the VDA ports allowed, usually 2598 (CGP).
	UpstreamPorts ICAPortList `json:"upstream_ports,omitempty" label:"VDA ports" placeholder:"2598" help:"Allowed VDA ports; 2598 is CGP." ui:"required"`
}

// ICAPortList is a list of ports that decodes from JSON numbers or strings,
// so `upstream_ports: [2598]` and `["2598"]` both load. It is a string
// slice because the control plane's form renders only string lists;
// validate parses each entry.
type ICAPortList []string

func (p *ICAPortList) UnmarshalJSON(data []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	out := make(ICAPortList, 0, len(raw))
	for _, item := range raw {
		var s string
		if err := json.Unmarshal(item, &s); err == nil {
			out = append(out, s)
			continue
		}
		var n int64
		if err := json.Unmarshal(item, &n); err != nil {
			return fmt.Errorf("upstream_ports: %s is neither a number nor a string", string(item))
		}
		out = append(out, strconv.FormatInt(n, 10))
	}
	*p = out
	return nil
}

// ports parses the list; validate has already refused anything invalid.
func (p ICAPortList) ports() ([]uint16, error) {
	out := make([]uint16, 0, len(p))
	for _, s := range p {
		n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 16)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("%q is not a port", s)
		}
		out = append(out, uint16(n))
	}
	return out, nil
}

// ICADisplayConfig pins session geometry.
type ICADisplayConfig struct {
	Width           int `json:"width,omitempty" label:"Width" placeholder:"1264" help:"Framebuffer width, 320..4096."`
	Height          int `json:"height,omitempty" label:"Height" placeholder:"1040" help:"Framebuffer height, 200..4096."`
	ThinwireChannel int `json:"thinwire_channel,omitempty" label:"ThinWire channel" placeholder:"9" help:"The ICA virtual channel carrying ThinWire video, 1..255."`
}

// validate takes the listener as well as the block, because the listener's
// `upstream` or the block's `transparent` selects the mode.
func (c *ICAConfig) validate(lc ListenerConfig, name string) []string {
	if c == nil {
		return []string{name + ": an ica listener needs an \"ica\" block with at least tenant_id"}
	}
	var problems []string
	if t := strings.TrimSpace(c.TenantID); t == "" || len(t) > 128 {
		problems = append(problems, name+": ica.tenant_id is required and at most 128 characters")
	}
	switch {
	case c.Transparent != nil && lc.Upstream != "":
		problems = append(problems, name+": upstream and ica.transparent are two modes; keep one: a fixed VDA, or the VDA each connection was destined to")
	case c.Transparent == nil && lc.Upstream == "":
		problems = append(problems, name+": an ica listener names a single VDA in upstream, or selects it per connection with ica.transparent")
	}
	if c.Transparent != nil {
		if lc.Network == "unix" {
			problems = append(problems, name+": ica.transparent intercepts TCP; network: unix cannot carry it")
		}
		if len(c.SourceCIDRs) != 0 {
			problems = append(problems, name+": ica.source_cidrs is single mode's; CIDR mode names its sources in ica.transparent.source_cidrs")
		}
		problems = append(problems, c.Transparent.validate(name)...)
	} else if _, err := parsePrefixes(c.SourceCIDRs); err != nil {
		problems = append(problems, fmt.Sprintf("%s: ica.source_cidrs: %v", name, err))
	}
	if c.EDT && lc.Network == "unix" {
		problems = append(problems, name+": ica.edt listens on UDP; network: unix cannot carry it")
	}
	if c.Display != nil {
		d := c.Display
		if d.Width != 0 && (d.Width < 320 || d.Width > 4096) {
			problems = append(problems, name+": ica.display.width must be 320..4096")
		}
		if d.Height != 0 && (d.Height < 200 || d.Height > 4096) {
			problems = append(problems, name+": ica.display.height must be 200..4096")
		}
		if d.ThinwireChannel != 0 && (d.ThinwireChannel < 1 || d.ThinwireChannel > 255) {
			problems = append(problems, name+": ica.display.thinwire_channel must be 1..255")
		}
	}
	return problems
}

func (t *ICATransparentConfig) validate(name string) []string {
	var problems []string
	if len(t.SourceCIDRs) == 0 {
		problems = append(problems, name+": ica.transparent.source_cidrs is required; a transparent listener admits only the networks it names")
	}
	if len(t.UpstreamCIDRs) == 0 {
		problems = append(problems, name+": ica.transparent.upstream_cidrs is required")
	}
	if len(t.UpstreamPorts) == 0 {
		problems = append(problems, name+": ica.transparent.upstream_ports is required; 2598 is CGP")
	}
	for _, key := range []struct {
		name string
		list []string
	}{{"source_cidrs", t.SourceCIDRs}, {"upstream_cidrs", t.UpstreamCIDRs}} {
		if _, err := parsePrefixes(key.list); err != nil {
			problems = append(problems, fmt.Sprintf("%s: ica.transparent.%s: %v", name, key.name, err))
		}
	}
	if _, err := t.UpstreamPorts.ports(); err != nil {
		problems = append(problems, fmt.Sprintf("%s: ica.transparent.upstream_ports: %v", name, err))
	}
	return problems
}

// parsePrefixes refuses a blank entry: dropping it would let a list of
// blanks pass validation and reach the engine as an empty policy.
func parsePrefixes(values []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil, errors.New("an entry is blank")
		}
		p, err := netip.ParsePrefix(v)
		if err != nil {
			return nil, fmt.Errorf("%q is not a CIDR", v)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// ICALane is the daemon's input to the engine for one lane. The daemon
// resolves every value, and the struct holds no config text and no engine
// types. Events is the sidecar's audit bridge; the engine calls it for each
// event it emits. A non-nil error from Events means the trail could not
// record a session start on a fail-closed lane, and the engine side must
// stop that session.
type ICALane struct {
	Name     string
	Listen   string
	Network  string // "" or "tcp", or "unix" in single mode
	TenantID string

	// Transparent selects CIDR mode, where SourcePrefixes, UpstreamPrefixes
	// and UpstreamPorts hold the policy and Upstream is empty. In single
	// mode, Upstream is the configured VDA (host:port; the engine resolves a
	// hostname once, at start) and SourcePrefixes is ica.source_cidrs, which
	// when empty admits any source.
	Transparent      bool
	Upstream         string
	SourcePrefixes   []netip.Prefix
	UpstreamPrefixes []netip.Prefix
	UpstreamPorts    []uint16

	Width, Height   int  // 0 = engine default
	ThinwireChannel uint // 0 = engine default

	// EDT opens a UDP listener on Listen beside the TCP one and serves EDT
	// sessions from it under the same policy.
	EDT bool

	Events func(ICAEvent) error
	Log    *slog.Logger
}

// ICARuntime is the resolved top-level block: paths and bounds as the
// operator wrote them (empty = engine default) plus the overrides.
type ICARuntime struct {
	OCRTurboLibrary, OCRTurboDetModel, OCRTurboRecModel, OCRTurboRecDict, OCRTurboClsModel string
	OCRWorker, Tessdata, NERModel, NERORTLibrary                                           string
	FailureDiagnosticDir                                                                   string
	MaxSessions, ConnectTimeoutSec                                                         int
	Options                                                                                ICAOptions
	// EDTRetentionSec and EDTReplayBudgetBytes are 0 for the engine's
	// defaults.
	EDTRetentionSec      int
	EDTReplayBudgetBytes int64
}

// ICAEvent is one engine audit event, as the engine shapes it: Name is
// the engine's event name, Data flat metadata. It carries no pixels and no
// recognized text. SessionID is empty for the pre-session events.
type ICAEvent struct {
	Time      time.Time
	Name      string
	SessionID string
	Direction string
	Data      map[string]any
}

// ICAServer is the running side of an ica lane. It satisfies
// endpointServer (Serve blocks until ctx ends or the listener fails; Close
// is idempotent and drains) and adds readiness, because the runtime loads
// for minutes after the socket binds.
//
// The builder MUST NOT bind a socket or load a model: Validate constructs
// the server on the control plane and on macOS. Both happen in Serve.
type ICAServer interface {
	Serve(ctx context.Context) error
	Close() error
	Addr() net.Addr
	Stats() (active, total, denied int64)
	Notes() []string
	// Ready reports whether the OCR/NER runtime is loaded. It returns false
	// before Serve and while the model loads.
	Ready() bool
}

// ICABuilder builds the server for one lane. sidecar/ica registers one
// from init; a binary gets the engine by importing that module.
type ICABuilder func(rt ICARuntime, lane ICALane) (ICAServer, error)

// ICAHostCheck reports whether this host can run the engine now: the OCR
// worker, the model and the inference library are where the engine's
// defaults read them. A build may link the engine and still run on a host
// without those (the hoop CLI on a database host), and such a process must
// not advertise protocol:ica to a control plane, which would then hand it
// a lane that fails at startup. The error names what is missing.
type ICAHostCheck func() error

var (
	icaMu        sync.Mutex
	icaBuild     ICABuilder
	icaHostCheck ICAHostCheck
)

// RegisterICAEngine links an ICA engine into this binary. It panics on a
// second registration, so the engine choice cannot depend on import order.
// hostCheck may be nil for an engine that needs nothing on the host.
func RegisterICAEngine(build ICABuilder, hostCheck ICAHostCheck) {
	if build == nil {
		panic("sidecar/daemon: RegisterICAEngine called with a nil builder")
	}
	icaMu.Lock()
	defer icaMu.Unlock()
	if icaBuild != nil {
		panic("sidecar/daemon: duplicate ICA engine registration")
	}
	icaBuild = build
	icaHostCheck = hostCheck
}

func registeredICAEngine() (ICABuilder, ICAHostCheck) {
	icaMu.Lock()
	defer icaMu.Unlock()
	return icaBuild, icaHostCheck
}

// icaEngineLinked reports whether this build carries an ICA engine. Run
// and Validate refuse an ica lane without one; with one, the engine
// itself reports a host that lacks its assets when the lane starts.
func icaEngineLinked() bool {
	build, _ := registeredICAEngine()
	return build != nil
}

// icaServable reports whether this process can serve an ica lane here:
// the engine is linked and its host check passes. ServableProtocols uses
// it for the capabilities header, so a plane does not serve an ica lane
// to a process that would fail it at startup.
func icaServable() bool {
	build, check := registeredICAEngine()
	return build != nil && (check == nil || check() == nil)
}

// checkICA refuses an ica lane this process cannot serve. Run and Validate
// call it; Config.Validate does not, because the control plane validates
// sidecar documents with the same decoder and links no engine.
func checkICA(cfg *Config) error {
	if icaEngineLinked() {
		return nil
	}
	for i, l := range cfg.Listeners {
		if isICA(l) {
			return fmt.Errorf("%s: protocol ica needs the ICA engine, and this build did not link it; "+
				"use the hoop-inspect binary, which imports sidecar/ica", l.displayName(i))
		}
	}
	return nil
}

// icaRuntimeFor resolves the top-level block, which may be absent.
func icaRuntimeFor(cfg *Config) ICARuntime {
	r := cfg.ICA
	if r == nil {
		return ICARuntime{}
	}
	out := ICARuntime{OCRTurboLibrary: r.OCRTurboLibrary, OCRTurboDetModel: r.OCRTurboDetModel,
		OCRTurboRecModel: r.OCRTurboRecModel, OCRTurboRecDict: r.OCRTurboRecDict, OCRTurboClsModel: r.OCRTurboClsModel,
		OCRWorker: r.OCRWorker, Tessdata: r.Tessdata, NERModel: r.NERModel, NERORTLibrary: r.NERORTLibrary,
		FailureDiagnosticDir: r.FailureDiagnosticDir, MaxSessions: r.MaxSessions, ConnectTimeoutSec: r.ConnectTimeoutSec}
	if r.Options != nil {
		out.Options = *r.Options
	}
	if r.EDT != nil {
		out.EDTRetentionSec, out.EDTReplayBudgetBytes = r.EDT.RetentionSec, r.EDT.ReplayBudgetBytes
	}
	return out
}

// resolveICALane turns a validated listener into the engine's input.
func resolveICALane(lc ListenerConfig, name string, events func(ICAEvent) error, log *slog.Logger) (ICALane, error) {
	ic := lc.ICA
	if ic == nil {
		return ICALane{}, fmt.Errorf("%s: protocol is ica but there is no ica block", name)
	}
	lane := ICALane{Name: name, Listen: lc.Listen, Network: lc.Network, TenantID: strings.TrimSpace(ic.TenantID),
		EDT: ic.EDT, Events: events, Log: log}
	if ic.Display != nil {
		lane.Width, lane.Height, lane.ThinwireChannel = ic.Display.Width, ic.Display.Height, uint(ic.Display.ThinwireChannel)
	}
	if ic.Transparent == nil {
		lane.Upstream = lc.Upstream
		var err error
		if lane.SourcePrefixes, err = parsePrefixes(ic.SourceCIDRs); err != nil {
			return ICALane{}, fmt.Errorf("%s: ica.source_cidrs: %w", name, err)
		}
		return lane, nil
	}
	lane.Transparent = true
	var err error
	if lane.SourcePrefixes, err = parsePrefixes(ic.Transparent.SourceCIDRs); err != nil {
		return ICALane{}, fmt.Errorf("%s: ica.transparent.source_cidrs: %w", name, err)
	}
	if lane.UpstreamPrefixes, err = parsePrefixes(ic.Transparent.UpstreamCIDRs); err != nil {
		return ICALane{}, fmt.Errorf("%s: ica.transparent.upstream_cidrs: %w", name, err)
	}
	if lane.UpstreamPorts, err = ic.Transparent.UpstreamPorts.ports(); err != nil {
		return ICALane{}, fmt.Errorf("%s: ica.transparent.upstream_ports: %w", name, err)
	}
	return lane, nil
}

// buildICAServer resolves one ica lane and builds the engine's server with
// the sidecar's audit bridge. It follows buildSSHServer and returns a
// liveRules so the reload loop handles every lane the same way, although an
// ica lane carries no rules (Validate refuses guardrails, mask, analyzer and
// opa on it).
func buildICAServer(ln lane, rt ICARuntime, ac AuditConfig, sink audit.Sink, log *slog.Logger) (ICAServer, *liveRules, error) {
	build, _ := registeredICAEngine()
	if build == nil {
		return nil, nil, fmt.Errorf("%s: protocol ica needs the ICA engine, and this build did not link it", ln.name)
	}
	lc := ln.cfg
	laneLog := log.With("listener", ln.name)
	bridge := newICAAuditBridge(sink, ln.name, strings.TrimSpace(lc.ICA.TenantID), ac.failOnAuditError(), laneLog)
	resolved, err := resolveICALane(lc, ln.name, bridge.observe, laneLog)
	if err != nil {
		return nil, nil, err
	}
	srv, err := build(rt, resolved)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w", ln.name, err)
	}
	return srv, newLiveRules(ln), nil
}

// icaLaneNotes returns the notes a -validate run reports for an ica lane
// that the engine cannot report: the mode, that CIDR mode is Linux-only and
// privileged, and what EDT needs from the network.
func icaLaneNotes(lc ListenerConfig) []string {
	ic := lc.ICA
	if ic == nil {
		return nil
	}
	var notes []string
	if ic.Transparent == nil {
		notes = append(notes, fmt.Sprintf("ica: single-VDA mode; every session goes to %s, and the engine resolves that name once at start", lc.Upstream))
	} else {
		notes = append(notes, fmt.Sprintf("ica: CIDR mode; the VDA is each connection's original destination within %s on ports %s, admitted from %s",
			strings.Join(ic.Transparent.UpstreamCIDRs, ", "), strings.Join(ic.Transparent.UpstreamPorts, ", "), strings.Join(ic.Transparent.SourceCIDRs, ", ")))
		notes = append(notes, "ica: CIDR mode needs Linux IP_TRANSPARENT, CAP_NET_ADMIN on this process, and the TPROXY rule and policy route on the host; the listener's own address must not fall inside the VDA CIDRs")
	}
	if ic.EDT {
		edt := fmt.Sprintf("ica: EDT (experimental) on UDP %s beside TCP; publish the port for UDP as well, or clients fall back to TCP", lc.Listen)
		if ic.Transparent != nil {
			edt = fmt.Sprintf("ica: EDT (experimental) on UDP %s beside TCP; the TPROXY rule must intercept UDP on the VDA ports too", lc.Listen)
		}
		notes = append(notes, edt)
	}
	return notes
}

// icaAuditBridge turns engine events into the sidecar's audit trail. The
// bridge writes one row per lifecycle event and none per frame: a session
// can produce thousands of frame decisions a minute, and the trail records
// who, where and what happened, without per-frame telemetry. The session
// end row carries the counts.
//
// Principal is the tenant: an ICA session authenticates to the VDA, and the
// sidecar does not see a user name. SessionID is the engine's id, which the
// engine guarantees unique per process.
//
// The lane's /stats denied count lives in the engine adapter, which sees
// the same events; the bridge keeps no second counter.
type icaAuditBridge struct {
	sink     audit.Sink
	lane     string
	tenant   string
	failHard bool
	log      *slog.Logger
	mu       sync.Mutex
	sessions map[string]*icaSessionTally
}

type icaSessionTally struct {
	started time.Time
	frames  int
	ocr     int
}

func newICAAuditBridge(sink audit.Sink, lane, tenant string, failHard bool, log *slog.Logger) *icaAuditBridge {
	return &icaAuditBridge{sink: sink, lane: lane, tenant: tenant, failHard: failHard, log: log,
		sessions: map[string]*icaSessionTally{}}
}

// Event names and data keys the bridge handles, spelled as the engine emits
// them. libhoop-ica/ica exports the same constants; the nested module
// compiles against those, and this file cannot import them.
const (
	icaEventConnectionRejected = "libhoop_ica.connection_rejected"
	icaEventSessionStart       = "libhoop_ica.session_start"
	icaEventSessionError       = "libhoop_ica.session_error"
	icaEventFrameDecision      = "libhoop_ica.frame_decision"
	icaEventOCRResult          = "libhoop_ica.ocr_result"
	icaEventSessionEnd         = "libhoop_ica.session_end"
	icaDecisionBlock           = "block"
	icaDecisionError           = "error"
)

// errICAUnaudited is what observe returns when a fail-closed lane could not
// record a session start. The ssh and grpc lanes refuse the connection in
// that case; an ica session is already admitted when the engine reports it,
// so the adapter stops it instead.
var errICAUnaudited = errors.New("audit write failed on a fail-closed lane; the session must not run unrecorded")

// observe writes the row for e. It returns errICAUnaudited only for a
// session start that the sink refused on a fail-closed lane: a refused
// connection never became a session, and the rows after a start describe
// a session that has already run.
func (b *icaAuditBridge) observe(e ICAEvent) error {
	if b == nil || b.sink == nil {
		return nil
	}
	var out *audit.Event
	switch e.Name {
	case icaEventConnectionRejected:
		out = b.event(audit.KindViolation, e)
		out.Rule, _ = e.Data["stage"].(string)
		out.Message, _ = e.Data["error"].(string)
	case icaEventSessionStart:
		b.mu.Lock()
		b.sessions[e.SessionID] = &icaSessionTally{started: e.Time}
		b.mu.Unlock()
		out = b.event(audit.KindSessionStart, e)
		out.Allowed = true
	case icaEventFrameDecision, icaEventOCRResult:
		b.mu.Lock()
		if t := b.sessions[e.SessionID]; t != nil {
			if e.Name == icaEventFrameDecision {
				t.frames++
			} else {
				t.ocr++
			}
		}
		b.mu.Unlock()
		return nil
	case icaEventSessionError:
		out = b.event(audit.KindError, e)
		out.Error, _ = e.Data["error"].(string)
		out.Rule, _ = e.Data["stage"].(string)
	case icaEventSessionEnd:
		decision, _ := e.Data["decision"].(string)
		b.mu.Lock()
		tally := b.sessions[e.SessionID]
		delete(b.sessions, e.SessionID)
		b.mu.Unlock()
		kind := audit.KindSessionEnd
		if decision == icaDecisionBlock {
			kind = audit.KindViolation
		}
		out = b.event(kind, e)
		out.Allowed = decision != icaDecisionBlock && decision != icaDecisionError
		out.Rule = decision
		if tally != nil {
			out.Duration = e.Time.Sub(tally.started)
			out.StatementCount = tally.frames
			out.Metadata["frames"] = fmt.Sprint(tally.frames)
			out.Metadata["ocr_batches"] = fmt.Sprint(tally.ocr)
		}
		if decision == icaDecisionBlock {
			out.DeniedCount = 1
		}
	default:
		return nil
	}
	err := b.sink.Write(context.Background(), *out)
	if err == nil {
		return nil
	}
	if !b.failHard {
		b.log.Warn("audit write failed", "event", e.Name, "error", err)
		return nil
	}
	b.log.Error("audit write failed", "event", e.Name, "error", err)
	if e.Name == icaEventSessionStart {
		return errICAUnaudited
	}
	return nil
}

func (b *icaAuditBridge) event(kind audit.Kind, e ICAEvent) *audit.Event {
	ts := e.Time
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	out := &audit.Event{
		Kind: kind, Timestamp: ts, SessionID: session.ID(e.SessionID), Principal: b.tenant,
		Protocol: inspect.ICA, Connection: b.lane, Metadata: map[string]string{},
	}
	// suppressed and dropped ride on EDT refusals: how many datagrams the
	// engine folded into this row, and how many refusal rows it could not
	// queue.
	for _, key := range []string{"decision", "stage", "transport", "upstream", "end_cause", "suppressed", "dropped"} {
		if v, ok := e.Data[key]; ok {
			out.Metadata[key] = fmt.Sprint(v)
		}
	}
	return out
}

// notReadyEndpoints returns the names of endpoint lanes whose engine has
// not loaded yet. It checks only servers that implement Ready; the other
// endpoint kinds are ready once bound. names[i] belongs to endpoints[i].
func notReadyEndpoints(endpoints []endpointServer, names []string) []string {
	var out []string
	for i, srv := range endpoints {
		if r, ok := srv.(interface{ Ready() bool }); ok && !r.Ready() {
			out = append(out, names[i])
		}
	}
	return out
}

// ServableProtocols returns the protocols this process can serve here:
// Protocols without ica when the build did not link the engine or the host
// lacks its assets (icaServable). The capabilities header lists these, so a
// plane does not hand this process a lane that would fail at startup, and
// the hoop CLI's setup wizard offers only these. Protocols keeps ica because
// the plane renders the form from it and the schema must match across builds.
func ServableProtocols() []string {
	out := Protocols()
	if icaServable() {
		return out
	}
	return slices.DeleteFunc(out, func(p string) bool { return p == string(inspect.ICA) })
}
