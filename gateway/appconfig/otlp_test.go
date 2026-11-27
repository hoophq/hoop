package appconfig

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var otlpEnvs = []string{
	"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_HEADERS",
	"OTEL_EXPORTER_OTLP_INSECURE", "OTEL_EXPORTER_OTLP_CERTIFICATE", "OTEL_EXPORTER_OTLP_TIMEOUT",
	"OTEL_METRIC_EXPORT_INTERVAL", "OTEL_TRACES_SAMPLER", "OTEL_TRACES_SAMPLER_ARG",
	"OTEL_SERVICE_NAME", "OTEL_RESOURCE_ATTRIBUTES",
}

func setOTLPEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, k := range otlpEnvs {
		t.Setenv(k, env[k])
	}
}

func TestLoadOTLPExportIsOffWithoutAnEndpoint(t *testing.T) {
	setOTLPEnv(t, map[string]string{"OTEL_EXPORTER_OTLP_HEADERS": "a=b", "OTEL_TRACES_SAMPLER": "always_on"})
	cfg, err := loadOTLPExport()
	require.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestLoadOTLPExport(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  map[string]string
		want OTLPExport
	}{
		{
			name: "grpc host:port, as the sidecar block writes it",
			env:  map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "otel-collector:4317"},
			want: OTLPExport{Protocol: OTLPProtocolGRPC, Host: "otel-collector:4317"},
		},
		{
			name: "grpc URL, as the spec writes it: http is in the clear",
			env:  map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "http://otel-collector:4317"},
			want: OTLPExport{Protocol: OTLPProtocolGRPC, Host: "otel-collector:4317", Insecure: true},
		},
		{
			name: "https decides TLS over OTEL_EXPORTER_OTLP_INSECURE, and a URL without a port means 443",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "https://api.vendor.example",
				"OTEL_EXPORTER_OTLP_INSECURE": "true",
			},
			want: OTLPExport{Protocol: OTLPProtocolGRPC, Host: "api.vendor.example:443"},
		},
		{
			name: "http with a path prefix",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "https://vendor.example:443/otlp/",
				"OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf",
			},
			want: OTLPExport{Protocol: OTLPProtocolHTTP, Host: "vendor.example:443", Path: "/otlp"},
		},
		{
			name: "the sidecar's spelling of http",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://otel-collector:4318",
				"OTEL_EXPORTER_OTLP_PROTOCOL": "http",
			},
			want: OTLPExport{Protocol: OTLPProtocolHTTP, Host: "otel-collector:4318", Insecure: true},
		},
		{
			name: "every knob",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "otel-collector:4317",
				"OTEL_EXPORTER_OTLP_INSECURE": "true",
				"OTEL_EXPORTER_OTLP_HEADERS":  "authorization=Bearer%20abc, x-team = core",
				"OTEL_EXPORTER_OTLP_TIMEOUT":  "2500",
				"OTEL_METRIC_EXPORT_INTERVAL": "15000",
				"OTEL_TRACES_SAMPLER":         "parentbased_traceidratio",
				"OTEL_TRACES_SAMPLER_ARG":     "0.05",
				"OTEL_SERVICE_NAME":           "hoop-control-plane",
				"OTEL_RESOURCE_ATTRIBUTES":    "deployment.environment=prod",
			},
			want: OTLPExport{
				Protocol: OTLPProtocolGRPC, Host: "otel-collector:4317", Insecure: true,
				Headers: map[string]string{"authorization": "Bearer abc", "x-team": "core"},
				Timeout: 2500 * time.Millisecond, Interval: 15 * time.Second,
				Sampler: "parentbased_traceidratio", SamplerArg: 0.05,
				ServiceName: "hoop-control-plane", Resource: map[string]string{"deployment.environment": "prod"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setOTLPEnv(t, tc.env)
			got, err := loadOTLPExport()
			require.NoError(t, err)
			require.NotNil(t, got)
			want := tc.want
			if want.Timeout == 0 {
				want.Timeout = 10 * time.Second
			}
			if want.Interval == 0 {
				want.Interval = 60 * time.Second
			}
			if want.SamplerArg == 0 {
				want.SamplerArg = 1
			}
			assert.Equal(t, want, *got)
		})
	}
}

// A malformed value stops startup, and the error names the variable.
func TestLoadOTLPExportRefusesMalformedValues(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want string
	}{
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "otel-collector"}, "OTEL_EXPORTER_OTLP_ENDPOINT"},
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "ftp://otel-collector:4317"}, "OTEL_EXPORTER_OTLP_ENDPOINT"},
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "otel-collector:4318", "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"}, "OTEL_EXPORTER_OTLP_ENDPOINT"},
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "c:4317", "OTEL_EXPORTER_OTLP_PROTOCOL": "http/json"}, "OTEL_EXPORTER_OTLP_PROTOCOL"},
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "c:4317", "OTEL_EXPORTER_OTLP_INSECURE": "yes please"}, "OTEL_EXPORTER_OTLP_INSECURE"},
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "c:4317", "OTEL_EXPORTER_OTLP_TIMEOUT": "10s"}, "OTEL_EXPORTER_OTLP_TIMEOUT"},
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "c:4317", "OTEL_METRIC_EXPORT_INTERVAL": "0"}, "OTEL_METRIC_EXPORT_INTERVAL"},
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "c:4317", "OTEL_TRACES_SAMPLER": "sometimes"}, "OTEL_TRACES_SAMPLER"},
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "c:4317", "OTEL_TRACES_SAMPLER_ARG": "2"}, "OTEL_TRACES_SAMPLER_ARG"},
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "c:4317", "OTEL_RESOURCE_ATTRIBUTES": "a=%zz"}, "OTEL_RESOURCE_ATTRIBUTES"},
		{map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "c:4317", "OTEL_EXPORTER_OTLP_CERTIFICATE": "/nonexistent/ca.pem"}, "OTEL_EXPORTER_OTLP_CERTIFICATE"},
	} {
		setOTLPEnv(t, tc.env)
		_, err := loadOTLPExport()
		if assert.Error(t, err, "%v", tc.env) {
			assert.Contains(t, err.Error(), tc.want)
		}
	}
}

// A header value is often a token: a malformed entry is named by position.
func TestLoadOTLPExportDoesNotPrintAHeaderValue(t *testing.T) {
	setOTLPEnv(t, map[string]string{
		"OTEL_EXPORTER_OTLP_ENDPOINT": "c:4317",
		"OTEL_EXPORTER_OTLP_HEADERS":  "x-team=core,Bearer s3cr3t",
	})
	_, err := loadOTLPExport()
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "s3cr3t")
	assert.Contains(t, err.Error(), "entry 2")
}

func TestLoadOTLPExportReadsTheCA(t *testing.T) {
	dir := t.TempDir()
	notPEM := filepath.Join(dir, "not.pem")
	require.NoError(t, os.WriteFile(notPEM, []byte("hello"), 0o600))
	setOTLPEnv(t, map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "c:4317", "OTEL_EXPORTER_OTLP_CERTIFICATE": notPEM})
	_, err := loadOTLPExport()
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "no PEM certificate"), err.Error())

	ca := filepath.Join(dir, "ca.pem")
	require.NoError(t, os.WriteFile(ca, []byte(testCertPEM), 0o600))
	setOTLPEnv(t, map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "c:4317", "OTEL_EXPORTER_OTLP_CERTIFICATE": ca})
	cfg, err := loadOTLPExport()
	require.NoError(t, err)
	assert.Equal(t, ca, cfg.CAFile)
}

// The other OTEL_* variables are read only once the endpoint turns the
// export on: a stray malformed one does not stop a gateway that exports
// nothing.
func TestLoadOTLPExportIgnoresMalformedValuesWithoutAnEndpoint(t *testing.T) {
	setOTLPEnv(t, map[string]string{
		"OTEL_EXPORTER_OTLP_PROTOCOL": "http/json",
		"OTEL_EXPORTER_OTLP_TIMEOUT":  "soon",
		"OTEL_TRACES_SAMPLER":         "sometimes",
		"OTEL_TRACES_SAMPLER_ARG":     "NaN",
	})
	cfg, err := loadOTLPExport()
	require.NoError(t, err)
	assert.Nil(t, cfg)
}

// An endpoint can carry a credential, and the error reaches the startup
// log: no error repeats the value.
func TestLoadOTLPExportDoesNotPrintTheEndpoint(t *testing.T) {
	for name, tc := range map[string]struct{ endpoint, want string }{
		"userinfo":       {"https://user:s3cr3t@collector:4317", "user or password"},
		"query token":    {"https://collector:4318/v1?token=s3cr3t", "query"},
		"malformed URL":  {"http://[::1%s3cr3t", "not a valid URL"},
		"host:port user": {"s3cr3t@collector:4317", "host:port"},
		"no host":        {"https://:4317/s3cr3t", "no host"},
		"other scheme":   {"s3cr3t://collector:4317", "scheme"},
	} {
		t.Run(name, func(t *testing.T) {
			setOTLPEnv(t, map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": tc.endpoint})
			_, err := loadOTLPExport()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "OTEL_EXPORTER_OTLP_ENDPOINT")
			assert.Contains(t, err.Error(), tc.want)
			assert.NotContains(t, err.Error(), "s3cr3t")
		})
	}
}

func TestLoadOTLPExportBoundsNumbers(t *testing.T) {
	base := map[string]string{"OTEL_EXPORTER_OTLP_ENDPOINT": "c:4317"}
	with := func(k, v string) map[string]string {
		m := map[string]string{k: v}
		for bk, bv := range base {
			m[bk] = bv
		}
		return m
	}
	for _, v := range []string{"NaN", "nan", "Inf", "-Inf", "-0.01", "1.01"} {
		setOTLPEnv(t, with("OTEL_TRACES_SAMPLER_ARG", v))
		_, err := loadOTLPExport()
		assert.Error(t, err, "OTEL_TRACES_SAMPLER_ARG=%s", v)
	}
	for _, v := range []string{"0", "1", "0.5"} {
		setOTLPEnv(t, with("OTEL_TRACES_SAMPLER_ARG", v))
		_, err := loadOTLPExport()
		assert.NoError(t, err, "OTEL_TRACES_SAMPLER_ARG=%s", v)
	}

	maxMillis := strconv.FormatInt(maxOTLPMillis, 10)
	overMax := strconv.FormatInt(maxOTLPMillis+1, 10)
	for _, env := range []string{"OTEL_EXPORTER_OTLP_TIMEOUT", "OTEL_METRIC_EXPORT_INTERVAL"} {
		setOTLPEnv(t, with(env, maxMillis))
		cfg, err := loadOTLPExport()
		require.NoError(t, err, env)
		assert.Positive(t, cfg.Timeout, env)
		assert.Positive(t, cfg.Interval, env)

		for _, v := range []string{overMax, "9223372036854775808", "-1", "0"} {
			setOTLPEnv(t, with(env, v))
			_, err := loadOTLPExport()
			assert.Error(t, err, "%s=%s", env, v)
		}
	}
}
