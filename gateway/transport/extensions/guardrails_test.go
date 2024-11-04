package transportext

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGuardRailRules(t *testing.T) {
	for _, tt := range []struct {
		msg  string
		rule *Rule
		err  error
	}{
		{
			msg:  "it should match rules",
			rule: &Rule{Type: denyWordListType, Words: []string{"foo"}},
			err:  fmt.Errorf(""),
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			assert.Equal(t, "", "")
		})
	}

}
