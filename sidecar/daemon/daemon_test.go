package daemon

import (
	"flag"
	"testing"
)

// The first-run default engages only when the user typed nothing at all.
// The gate keys on what was typed, not on resulting values: an explicit
// -validate=false or -license= parses to the default value but is still a
// mistake to report when no config exists, and a positional argument is
// never a request for the demo.
func TestBareInvocation(t *testing.T) {
	for _, tt := range []struct {
		msg  string
		args []string
		want bool
	}{
		{msg: "nothing at all is bare", want: true},
		{msg: "a set flag is not bare", args: []string{"-validate"}},
		{msg: "an explicit false flag is not bare", args: []string{"-validate=false"}},
		{msg: "an explicit empty value is not bare", args: []string{"-license="}},
		{msg: "a positional argument is not bare", args: []string{"extra"}},
		{msg: "a flag and an argument are not bare", args: []string{"-strict", "extra"}},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			// The same flags Main declares, minus the ones the gate never
			// sees (-config non-empty and -version return before it).
			fs := flag.NewFlagSet("hoop-inspect", flag.ContinueOnError)
			fs.Bool("validate", false, "")
			fs.Bool("strict", false, "")
			fs.String("license", "", "")
			fs.String("token", "", "")
			if err := fs.Parse(tt.args); err != nil {
				t.Fatalf("parse %v: %v", tt.args, err)
			}
			if got := bareInvocation(fs); got != tt.want {
				t.Errorf("bareInvocation(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}
