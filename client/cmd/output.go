package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/hoophq/hoop/client/cmd/styles"
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

// fatalErr prints a styled error (or JSON error event) and exits with code 1.
func fatalErr(jsonMode bool, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if jsonMode {
		exitCode := 1
		emitJSONEvent(os.Stdout, JSONEvent{Status: "error", Message: msg, ExitCode: &exitCode})
	} else {
		fmt.Println(styles.ClientError(msg))
	}
	os.Exit(1)
}
