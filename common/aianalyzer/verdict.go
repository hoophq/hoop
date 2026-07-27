package aianalyzer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/vmihailenco/msgpack/v5"
)

// VerdictInfoKey is the packet spec key under which the per-request analyzer
// verdict is attached so the gateway audit plugin can persist it. The same key
// and wire layout are mirrored in common/proto/spectypes (by value) so the
// audit plugin decodes it without importing this engine package.
const VerdictInfoKey = "aianalyzer.info"

// Verdict is the msgpack-encoded analyzer result attached to response packets.
//
// Seq and ConnID identify the originating request so the gateway can dedupe the
// verdict, which is sticky on the shared response spec and repeats across every
// response chunk. Seq is a monotonic, per-connection request counter; ConnID is
// the proxy connection id. Together they count each analyzed request once.
type Verdict struct {
	Outcome     string `msgpack:"outcome"`
	RiskLevel   string `msgpack:"risk_level"`
	Title       string `msgpack:"title"`
	Explanation string `msgpack:"explanation"`
	RuleName    string `msgpack:"rule_name"`
	Seq         uint64 `msgpack:"seq"`
	ConnID      string `msgpack:"conn_id"`
}

// Verdict converts a Decision into its wire form, stamping the per-connection
// sequence and connection id used by the gateway for deduplication.
func (d *Decision) Verdict(seq uint64, connID string) *Verdict {
	return &Verdict{
		Outcome:     string(d.Outcome),
		RiskLevel:   string(d.RiskLevel),
		Title:       d.Title,
		Explanation: d.Explanation,
		RuleName:    d.RuleName,
		Seq:         seq,
		ConnID:      connID,
	}
}

// Encode serializes the verdict for transport in a packet spec.
func (v *Verdict) Encode() ([]byte, error) {
	return msgpack.Marshal(v)
}

// RenderForbidden builds a self-contained HTTP 403 response for a blocked
// request. The proxy writes these bytes to the client while keeping the session
// open so subsequent requests are still analyzed and forwarded.
func RenderForbidden(d *Decision) []byte {
	payload, _ := json.Marshal(map[string]string{
		"error":       "request blocked by ai session analyzer",
		"risk_level":  string(d.RiskLevel),
		"title":       d.Title,
		"explanation": d.Explanation,
		"rule":        d.RuleName,
	})

	resp := &http.Response{
		Status:        "403 Forbidden",
		StatusCode:    http.StatusForbidden,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{},
		Body:          io.NopCloser(bytes.NewReader(payload)),
		ContentLength: int64(len(payload)),
	}
	resp.Header.Set("Content-Type", "application/json")
	resp.Header.Set("Content-Length", fmt.Sprintf("%d", len(payload)))

	var buf bytes.Buffer
	// http.Response.Write only fails on a writer error; bytes.Buffer never errors.
	_ = resp.Write(&buf)
	return buf.Bytes()
}
