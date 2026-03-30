package cmd

import (
	"encoding/json"
	"io"
)

// JSONEvent is the structured output format for agent-friendly CLI usage.
// Each state transition emits one JSON object per line to the output writer.
type JSONEvent struct {
	Status   string            `json:"status"`
	Message  string            `json:"message,omitempty"`
	Data     map[string]string `json:"data,omitempty"`
	ExitCode *int              `json:"exit_code,omitempty"`
}

// emitJSONEvent writes a single JSON event as one line to the given writer.
func emitJSONEvent(w io.Writer, event JSONEvent) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(event)
}
