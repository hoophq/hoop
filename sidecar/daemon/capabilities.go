package daemon

import (
	"fmt"
	"slices"
	"strings"

	"github.com/hoophq/hoop/sidecar/analyzer"
)

// CapabilitiesHeader lists, comma separated, the served-document features
// this build decodes. The sidecar sends it on the handshake.
//
// A header, like the answer's LicenseManagedHeader and ConfigRevisionHeader.
// Absent means a build too old to report; the plane keeps that apart from a
// build that reports none.
//
// The served document is decoded with DisallowUnknownFields, so a key this
// build does not declare fails the whole document. The plane reads this
// header to refuse such a key instead of serving it; see CheckServable.
const CapabilitiesHeader = "hoop-sidecar-capabilities"

// CapabilityReviewMode means this build decodes an analyzer block's
// review_mode.
const CapabilityReviewMode = "review_mode"

// CapabilityAnalyzerRateLimit means this build decodes analyzer rate_limit,
// on the top-level section and on a listener's block.
const CapabilityAnalyzerRateLimit = "analyzer_rate_limit"

// capabilitySince names the first release that sends each capability, for
// the refusal an admin reads. A hoop release, because `hoop start sidecar`
// is the shipped binary.
var capabilitySince = map[string]string{
	CapabilityReviewMode:        "1.191.0",
	CapabilityAnalyzerRateLimit: "1.198.0",
}

// SidecarCapabilities is what this build sends in CapabilitiesHeader.
func SidecarCapabilities() []string {
	return []string{CapabilityReviewMode, CapabilityAnalyzerRateLimit}
}

// ParseCapabilities reads a CapabilitiesHeader value. It never returns nil,
// so a caller can store "reported none" apart from "never reported".
func ParseCapabilities(header string) []string {
	out := []string{}
	for _, c := range strings.Split(header, ",") {
		if c = strings.TrimSpace(c); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// CheckServable refuses a document that a sidecar reporting caps cannot
// decode. The error names the listener and the release that adds support.
func CheckServable(cfg Config, caps []string) error {
	rateLimits := slices.Contains(caps, CapabilityAnalyzerRateLimit)
	if !rateLimits && cfg.Analyzer != nil && cfg.Analyzer.RateLimit != nil {
		return fmt.Errorf("the analyzer section sets rate_limit, and this sidecar does not "+
			"support it; upgrade the sidecar to %s or later, or remove rate_limit",
			capabilitySince[CapabilityAnalyzerRateLimit])
	}
	for _, l := range cfg.Listeners {
		if l.Analyzer == nil {
			continue
		}
		if !rateLimits && l.Analyzer.RateLimit != nil {
			return fmt.Errorf("listener %q sets analyzer rate_limit, and this sidecar does not "+
				"support it; upgrade the sidecar to %s or later, or remove rate_limit",
				l.Name, capabilitySince[CapabilityAnalyzerRateLimit])
		}
		if l.Analyzer.ReviewMode != analyzer.ReviewReturn {
			continue
		}
		if !slices.Contains(caps, CapabilityReviewMode) {
			return fmt.Errorf("listener %q sets review_mode %q, and this sidecar does not "+
				"support it; upgrade the sidecar to %s or later, or set review_mode to %q",
				l.Name, analyzer.ReviewReturn, capabilitySince[CapabilityReviewMode], analyzer.ReviewHold)
		}
	}
	return nil
}

// ServedForm drops the review_mode key where it holds the default, so a hold
// lane serves the same document an older build decodes. It copies what it
// changes: cfg may share listeners with a stored row.
func ServedForm(cfg Config) Config {
	listeners := make([]ListenerConfig, len(cfg.Listeners))
	copy(listeners, cfg.Listeners)
	for i, l := range listeners {
		if l.Analyzer == nil || l.Analyzer.ReviewMode != analyzer.ReviewHold {
			continue
		}
		block := *l.Analyzer
		block.ReviewMode = ""
		listeners[i].Analyzer = &block
	}
	cfg.Listeners = listeners
	return cfg
}
