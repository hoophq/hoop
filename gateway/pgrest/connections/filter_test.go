package pgconnections

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUrlEncodeOptions(t *testing.T) {
	optKeyFn := func(v string) []string { return []string{v} }
	for _, tt := range []struct {
		msg  string
		opts []*ConnectionOption
		want string
	}{
		{
			msg: "it must be able to encode all options",
			opts: []*ConnectionOption{
				WithOption(optKeyFn("type"), "database"),
				WithOption(optKeyFn("subtype"), "postgres"),
				WithOption(optKeyFn("managed_by"), "hoopagent"),
				WithOption([]string{"tags", "env"}, "production"),
				WithOption([]string{"tags", "team"}, "devops"),
			},
			want: "&type=eq.database&subtype=eq.postgres&managed_by=eq.hoopagent&tags->>env=eq.production&tags->>team=eq.devops",
		},
		{
			msg: "it must be able to encode empty value options as a null query expression",
			opts: []*ConnectionOption{
				WithOption(optKeyFn("type"), "database"),
				WithOption(optKeyFn("subtype"), "postgres"),
				WithOption(optKeyFn("managed_by"), ""),
				WithOption([]string{"tags", "env"}, ""),
			},
			want: "&type=eq.database&subtype=eq.postgres&managed_by=is.null&tags->>env=is.null",
		},
		{
			msg: "it must ignore unknown options",
			opts: []*ConnectionOption{
				WithOption(optKeyFn("unknown_option"), "val"),
				WithOption(optKeyFn("tags.foo.bar"), "val"),
			},
			want: "",
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			v := urlEncodeOptions(tt.opts)
			assert.Equal(t, tt.want, v)
		})
	}

}
