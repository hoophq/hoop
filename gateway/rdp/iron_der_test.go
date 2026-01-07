package rdp

import (
	"encoding/hex"
	"testing"
)

func TestUnmarshalContextExplicit_VersionUint64(t *testing.T) {
	// DER: SEQUENCE { [0] EXPLICIT INTEGER 42 }
	// Hex: 30 05 a0 03 02 01 2a
	d, _ := hex.DecodeString("3005a00302012a")
	var p RDCleanPathPdu
	if err := unmarshalContextExplicit(d, &p); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if p.Version != 42 {
		t.Fatalf("expected Version=42, got %d", p.Version)
	}
}

func TestMarshalContextExplicit_RoundtripUint64(t *testing.T) {
	in := RDCleanPathPdu{
		Version: 42,
	}
	der, err := marshalContextExplicit(in)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	var out RDCleanPathPdu
	if err := unmarshalContextExplicit(der, &out); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if out.Version != in.Version {
		t.Fatalf("roundtrip mismatch: got %d want %d", out.Version, in.Version)
	}
}
