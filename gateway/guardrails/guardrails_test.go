package guardrails

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGuardRailRules(t *testing.T) {
	for _, tt := range []struct {
		msg   string
		rule  *Rule
		input string
		err   error
	}{
		{
			msg:   "it should match deny word rules",
			rule:  &Rule{Type: denyWordListType, Words: []string{"foo"}},
			input: "has foo in the input",
			err:   fmt.Errorf("validation error, match guard rails <dunno> rule"),
		},
		{
			msg:  "it should skip empty words",
			rule: &Rule{Type: denyWordListType, Words: []string{""}},
			err:  nil,
		},
		{
			msg:   "it should match regex",
			rule:  &Rule{Type: patternMatchRegexType, PatternRegex: "^[A-Z0-9]+"},
			input: "ABC123",
			err:   fmt.Errorf("validation error, match guard rails <dunno> rule"),
		},
		{
			msg:  "it should skip empty regex",
			rule: &Rule{Type: patternMatchRegexType, PatternRegex: ""},
			err:  nil,
		},
		{
			msg:   "it should add a name as context to ther error",
			rule:  &Rule{Type: denyWordListType, Words: []string{"foo"}, Name: "deny-foo"},
			input: "foo",
			err:   fmt.Errorf("validation error, match guard rails <dunno> rule, name=deny-foo"),
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			err := tt.rule.validate("<dunno>", []byte(tt.input))
			if err != nil {
				assert.EqualError(t, err, tt.err.Error())
				return
			}
			assert.Nil(t, tt.err)
		})
	}

}
