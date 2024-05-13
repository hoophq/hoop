package cmd

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParsePosixCommand(t *testing.T) {
	for _, tt := range []struct {
		msg  string
		cmd  string
		want []string
		err  error
	}{
		{
			msg:  "it must match parsing single and double quotes",
			cmd:  `rails runner 'puts "Hello World"'`,
			want: []string{"rails", "runner", `'puts "Hello World"'`},
		},
		// kubectl exec --stdin deployment/myapp --
		{
			msg:  "simple: it must match with shell bultin delimiter --",
			cmd:  `kubectl exec -it deploy/myapp --`,
			want: []string{"kubectl", "exec", "-it", "deploy/myapp", "--"},
		},
		{
			msg:  "complex: it must match with shell bultin delimiter --",
			cmd:  `kubectl exec -it deploy/myapp -- rails runner 'puts "Hello World"'`,
			want: []string{"kubectl", "exec", "-it", "deploy/myapp", "--", "rails", "runner", `'puts "Hello World"'`},
		},
		{
			msg:  "it must match with env variables",
			cmd:  `psql -v ON_ERROR_STOP=1 -A -F\t -P pager=off -h $HOST -U $USER --port=$PORT $DB`,
			want: []string{"psql", "-v", "ON_ERROR_STOP=1", "-A", `-F\t`, "-P", "pager=off", "-h", "$HOST", "-U", "$USER", "--port=$PORT", "$DB"},
		},
		{
			msg:  "it must match with flags without space",
			cmd:  `sqlcmd --exit-on-error --trim-spaces -r -S$HOST:$PORT -U$USER -d$DB -i/dev/stdin`,
			want: []string{"sqlcmd", "--exit-on-error", "--trim-spaces", "-r", "-S$HOST:$PORT", "-U$USER", "-d$DB", "-i/dev/stdin"},
		},
		{
			msg: "it must fail when first statement is not call expression",
			cmd: "/tmp/script.sh && /tmp/script2.sh",
			err: errors.New("unable to coerce to CallExpr"),
		},
		{
			msg: "it must fail when there are multiple call expressions",
			cmd: "/tmp/script.sh; /tmp/script2.sh",
			err: fmt.Errorf("fail parsing command, empty or multiple statements found"),
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			got, err := parsePosixCmd(tt.cmd)

			if !cmp.Equal(fmt.Sprintf("%v", tt.err), fmt.Sprintf("%v", err)) {
				t.Errorf("expect error to match, got=%v, want=%v", err, tt.err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("not equal: %v", diff)
			}
		})
	}

}
