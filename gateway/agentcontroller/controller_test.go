package agentcontroller

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeOrgName(t *testing.T) {
	for _, tt := range []struct {
		msg  string
		want string
		val  string
		err  error
	}{
		{
			msg:  "match basic normalization",
			val:  "my-company-name",
			want: fmt.Sprintf("%s-mycompanyname", defaultPrefixAgentName),
		},
		{
			msg:  "match strip all spaces and lower case characteres",
			val:  "My Comp7ny Name",
			want: fmt.Sprintf("%s-mycomp7nyname", defaultPrefixAgentName),
		},
		{
			msg:  "it must removecharacters with accent",
			val:  "My Comp7ny Céu Ãberto Name",
			want: fmt.Sprintf("%s-mycomp7nyceuabertoname", defaultPrefixAgentName),
		},
		{
			msg:  "it must truncate if has more than 36 characteres",
			val:  "My Comp7ny Name With Big huHe Name hoh HAhzoio",
			want: fmt.Sprintf("%s-mycomp7nynamewithbighuhenamehohhahzo", defaultPrefixAgentName),
		},
		{
			msg:  "remove all special characters",
			val:  "My$ˆ%$#&*()_-+Special",
			want: fmt.Sprintf("%s-my_special", defaultPrefixAgentName),
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			got := normalizeOrgName(tt.val)
			assert.Equal(t, tt.want, got)
		})
	}
}
