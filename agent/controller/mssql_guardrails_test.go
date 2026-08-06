package controller

import (
	"testing"

	pb "github.com/hoophq/hoop/common/proto"
)

// TestAddMSSQLGuardRailsOpts locks in which opts the native MSSQL proxy forwards
// to libhoop. The MSSQL proxy enforces input guardrails only; it must forward
// the provider-selection keys so guardrail regex is evaluated through Presidio
// when configured, but must NOT forward the masking/GCP keys, which would push
// a GCP-DLP org onto libhoop's "guardrails require MSPresidio" fail-closed path.
func TestAddMSSQLGuardRailsOpts(t *testing.T) {
	connParams := &pb.AgentConnectionParams{
		DlpProvider:                "mspresidio",
		DlpMode:                    "best-effort",
		DlpPresidioAnalyzerURL:     "http://analyzer:3000",
		DlpPresidioAnonymizerURL:   "http://anonymizer:3000",
		DlpGcpRawCredentialsJSON:   `{"type":"service_account"}`,
		DLPInfoTypes:               []string{"EMAIL_ADDRESS"},
		DataMaskingEntityTypesData: []byte(`{"foo":"bar"}`),
		GuardRailRules:             []byte(`{"input_rules":[{"rules":[]}]}`),
	}

	opts := map[string]string{}
	addMSSQLGuardRailsOpts(opts, connParams)

	wantSet := map[string]string{
		"dlp_provider":              "mspresidio",
		"dlp_mode":                  "best-effort",
		"mspresidio_analyzer_url":   "http://analyzer:3000",
		"mspresidio_anonymizer_url": "http://anonymizer:3000",
		"guard_rail_rules":          `{"input_rules":[{"rules":[]}]}`,
	}
	for k, want := range wantSet {
		if got := opts[k]; got != want {
			t.Errorf("opts[%q] = %q, want %q", k, got, want)
		}
	}

	// Masking/GCP keys must never be forwarded: MSSQL does no output masking,
	// and their presence alongside guardrails trips libhoop's fail-closed path.
	forbidden := []string{
		"dlp_gcp_credentials",
		"dlp_info_types",
		"data_masking_entity_data",
		"dlp_masking_character",
	}
	for _, k := range forbidden {
		if _, ok := opts[k]; ok {
			t.Errorf("opts[%q] must not be forwarded to the MSSQL proxy", k)
		}
	}
}

// TestAddMSSQLGuardRailsOptsNilGuardRails verifies a connection without
// guardrail rules yields an empty guard_rail_rules value (plain passthrough),
// not a nil-deref.
func TestAddMSSQLGuardRailsOptsNilGuardRails(t *testing.T) {
	opts := map[string]string{}
	addMSSQLGuardRailsOpts(opts, &pb.AgentConnectionParams{})

	if got, ok := opts["guard_rail_rules"]; !ok || got != "" {
		t.Errorf(`guard_rail_rules = %q (present=%v), want "" present`, got, ok)
	}
}
