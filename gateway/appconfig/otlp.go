package appconfig

import (
	"crypto/x509"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// OTLP protocols, as HOOP_OTEL_EXPORTER_OTLP_PROTOCOL spells them. "http" is
// accepted for http/protobuf: it is the sidecar's spelling of the same
// transport.
const (
	OTLPProtocolGRPC = "grpc"
	OTLPProtocolHTTP = "http/protobuf"
)

// Trace samplers, as HOOP_OTEL_TRACES_SAMPLER spells them.
var otlpSamplers = map[string]bool{
	"always_on":                true,
	"always_off":               true,
	"traceidratio":             true,
	"parentbased_always_on":    true,
	"parentbased_always_off":   true,
	"parentbased_traceidratio": true,
}

// OTLPExport is where the gateway pushes its OpenTelemetry signals. It is
// read from the HOOP_OTEL_* variables, with the same knobs as the sidecar's
// telemetry block (protocol, endpoint, headers, TLS), so one collector
// serves both. Nil means export is off.
type OTLPExport struct {
	// Protocol is OTLPProtocolGRPC or OTLPProtocolHTTP.
	Protocol string
	// Host is host:port. Path is the URL path the http transport posts
	// under, before /v1/<signal>; empty for grpc.
	Host, Path string
	// Insecure sends in the clear.
	Insecure bool
	// CAFile pins the collector's CA. Empty is the host trust store.
	CAFile string
	// Headers go on every export request.
	Headers map[string]string
	// Timeout bounds one export; Interval is how often metrics go out.
	Timeout, Interval time.Duration
	// Sampler is a HOOP_OTEL_TRACES_SAMPLER value, SamplerArg its ratio.
	// Empty Sampler means no spans are sent.
	Sampler    string
	SamplerArg float64
	// ServiceName overrides the default service.name. Resource adds
	// attributes to every signal.
	ServiceName string
	Resource    map[string]string
}

// loadOTLPExport reads the HOOP_OTEL_* variables.
// HOOP_OTEL_EXPORTER_OTLP_ENDPOINT turns export on; every other variable
// only tunes it, and a malformed one stops startup like any other bad value
// here.
//
// The names are the standard OTEL_* ones with a HOOP_ prefix. The standard
// names are never read: a platform can inject them into every pod (the EKS
// CloudWatch Observability add-on does), and an injected value must neither
// stop the gateway nor turn its export on.
//
//	HOOP_OTEL_EXPORTER_OTLP_ENDPOINT     a URL; for grpc also host:port, as in the sidecar block
//	HOOP_OTEL_EXPORTER_OTLP_PROTOCOL     grpc (default) | http/protobuf
//	HOOP_OTEL_EXPORTER_OTLP_HEADERS      k=v,k2=v2, values URL-encoded
//	HOOP_OTEL_EXPORTER_OTLP_INSECURE     true sends a host:port endpoint in the clear; a URL's scheme decides for itself
//	HOOP_OTEL_EXPORTER_OTLP_CERTIFICATE  PEM file with the collector's CA
//	HOOP_OTEL_EXPORTER_OTLP_TIMEOUT      milliseconds, default 10000
//	HOOP_OTEL_METRIC_EXPORT_INTERVAL     milliseconds, default 60000
//	HOOP_OTEL_TRACES_SAMPLER             unset sends no spans; else a standard sampler name
//	HOOP_OTEL_TRACES_SAMPLER_ARG         the ratio of the *traceidratio samplers, 0 to 1
//	HOOP_OTEL_SERVICE_NAME               default hoop-gateway
//	HOOP_OTEL_RESOURCE_ATTRIBUTES        k=v,k2=v2, values URL-encoded
func loadOTLPExport() (*OTLPExport, error) {
	endpoint := os.Getenv("HOOP_OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		return nil, nil
	}
	cfg := &OTLPExport{
		Protocol:    OTLPProtocolGRPC,
		CAFile:      os.Getenv("HOOP_OTEL_EXPORTER_OTLP_CERTIFICATE"),
		ServiceName: os.Getenv("HOOP_OTEL_SERVICE_NAME"),
		Sampler:     os.Getenv("HOOP_OTEL_TRACES_SAMPLER"),
	}
	switch p := os.Getenv("HOOP_OTEL_EXPORTER_OTLP_PROTOCOL"); p {
	case "", OTLPProtocolGRPC:
	case OTLPProtocolHTTP, "http":
		cfg.Protocol = OTLPProtocolHTTP
	default:
		return nil, fmt.Errorf("invalid HOOP_OTEL_EXPORTER_OTLP_PROTOCOL %q (want grpc|http/protobuf)", p)
	}
	if v := os.Getenv("HOOP_OTEL_EXPORTER_OTLP_INSECURE"); v != "" {
		insecure, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("invalid HOOP_OTEL_EXPORTER_OTLP_INSECURE %q (want true|false)", v)
		}
		cfg.Insecure = insecure
	}
	if err := cfg.parseEndpoint(endpoint); err != nil {
		return nil, err
	}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed reading HOOP_OTEL_EXPORTER_OTLP_CERTIFICATE, reason=%v", err)
		}
		if !x509.NewCertPool().AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("HOOP_OTEL_EXPORTER_OTLP_CERTIFICATE %q holds no PEM certificate", cfg.CAFile)
		}
	}
	var err error
	if cfg.Headers, err = parseOTLPPairs("HOOP_OTEL_EXPORTER_OTLP_HEADERS"); err != nil {
		return nil, err
	}
	if cfg.Resource, err = parseOTLPPairs("HOOP_OTEL_RESOURCE_ATTRIBUTES"); err != nil {
		return nil, err
	}
	if cfg.Timeout, err = parseOTLPMillis("HOOP_OTEL_EXPORTER_OTLP_TIMEOUT", 10*time.Second); err != nil {
		return nil, err
	}
	if cfg.Interval, err = parseOTLPMillis("HOOP_OTEL_METRIC_EXPORT_INTERVAL", 60*time.Second); err != nil {
		return nil, err
	}
	if cfg.Sampler != "" && !otlpSamplers[cfg.Sampler] {
		return nil, fmt.Errorf("invalid HOOP_OTEL_TRACES_SAMPLER %q (want always_on|always_off|traceidratio|"+
			"parentbased_always_on|parentbased_always_off|parentbased_traceidratio)", cfg.Sampler)
	}
	cfg.SamplerArg = 1
	if v := os.Getenv("HOOP_OTEL_TRACES_SAMPLER_ARG"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		// Written so NaN fails it: every comparison with NaN is false.
		if err != nil || !(f >= 0 && f <= 1) {
			return nil, fmt.Errorf("invalid HOOP_OTEL_TRACES_SAMPLER_ARG %q (want a ratio from 0 to 1)", v)
		}
		cfg.SamplerArg = f
	}
	return cfg, nil
}

// parseEndpoint accepts what the OpenTelemetry spec says, a URL, and for
// grpc also host:port, the sidecar block's form, so one value serves both.
// A URL's scheme decides TLS, as the spec says: http:// sends in the clear
// and https:// never does, whatever HOOP_OTEL_EXPORTER_OTLP_INSECURE says.
// A URL without a port means the scheme's port, 80 or 443, as any URL does.
//
// No error names the value: an endpoint can carry a credential, and the
// error reaches the startup log.
func (c *OTLPExport) parseEndpoint(endpoint string) error {
	const env = "invalid HOOP_OTEL_EXPORTER_OTLP_ENDPOINT: "
	if !strings.Contains(endpoint, "://") {
		if c.Protocol == OTLPProtocolHTTP {
			return errors.New(env + "http/protobuf needs a URL, such as http://otel-collector:4318")
		}
		if strings.ContainsAny(endpoint, "@/?#") {
			return errors.New(env + "a host:port endpoint holds no user, path, query or fragment")
		}
		if host, _, err := net.SplitHostPort(endpoint); err != nil || host == "" {
			return errors.New(env + "want a URL or host:port, such as otel-collector:4317")
		}
		c.Host = endpoint
		return nil
	}
	// url.Parse's error quotes the input, so it is not wrapped.
	u, err := url.Parse(endpoint)
	switch {
	case err != nil:
		return errors.New(env + "not a valid URL")
	case u.User != nil:
		return errors.New(env + "the URL holds a user or password; send credentials in HOOP_OTEL_EXPORTER_OTLP_HEADERS")
	case u.RawQuery != "" || u.ForceQuery:
		return errors.New(env + "the URL holds a query; send credentials in HOOP_OTEL_EXPORTER_OTLP_HEADERS")
	case u.Fragment != "":
		return errors.New(env + "the URL holds a fragment")
	case u.Scheme != "http" && u.Scheme != "https":
		return errors.New(env + "the URL scheme is not http or https")
	case u.Hostname() == "":
		return errors.New(env + "the URL has no host, want one such as http://otel-collector:4318")
	}
	c.Host = u.Host
	if u.Port() == "" {
		port := "443"
		if u.Scheme == "http" {
			port = "80"
		}
		c.Host = net.JoinHostPort(u.Hostname(), port)
	}
	c.Insecure = u.Scheme == "http"
	if c.Protocol == OTLPProtocolHTTP {
		c.Path = strings.TrimSuffix(u.Path, "/")
	}
	return nil
}

// parseOTLPPairs reads a k=v,k2=v2 list, the W3C baggage form the OTel spec
// gives OTEL_EXPORTER_OTLP_HEADERS and OTEL_RESOURCE_ATTRIBUTES.
func parseOTLPPairs(env string) (map[string]string, error) {
	raw := os.Getenv(env)
	if raw == "" {
		return nil, nil
	}
	out := map[string]string{}
	for i, pair := range strings.Split(raw, ",") {
		k, v, ok := strings.Cut(pair, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			// The entry is not printed: a header value is often a token.
			return nil, fmt.Errorf("invalid %s: entry %d is not key=value", env, i+1)
		}
		value, err := url.PathUnescape(strings.TrimSpace(v))
		if err != nil {
			return nil, fmt.Errorf("invalid %s: the value of %q is not URL-encoded", env, k)
		}
		out[k] = value
	}
	return out, nil
}

// maxOTLPMillis is the most milliseconds a time.Duration holds.
const maxOTLPMillis = math.MaxInt64 / int64(time.Millisecond)

// parseOTLPMillis reads a positive count of milliseconds.
func parseOTLPMillis(env string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(env)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 1 || n > maxOTLPMillis {
		return 0, fmt.Errorf("invalid %s %q (want milliseconds, an integer from 1 to %d)", env, v, maxOTLPMillis)
	}
	return time.Duration(n) * time.Millisecond, nil
}
