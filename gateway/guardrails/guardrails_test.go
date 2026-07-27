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
			err: fmt.Errorf("validation error, match guard rails <dunno> rule, type=%v, words=%v",
				denyWordListType, []string{"foo"}),
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
			err: fmt.Errorf("validation error, match guard rails <dunno> rule, type=%v, pattern=%v",
				patternMatchRegexType, "^[A-Z0-9]+"),
		},
		{
			msg:  "it should skip empty regex",
			rule: &Rule{Type: patternMatchRegexType, PatternRegex: ""},
			err:  nil,
		},
		{
			msg:   "it should add a name as context to ther error",
			rule:  &Rule{Type: denyWordListType, Words: []string{"foo"}},
			input: "foo",
			err: fmt.Errorf("validation error, match guard rails <dunno> rule, type=%v, words=%v",
				denyWordListType, []string{"foo"}),
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

func TestMessageSurvivesDecode(t *testing.T) {
	dataRules, err := Decode([]byte(`[{"rules":[{"type":"deny_words_list","words":["SELECT"],"pattern_regex":"","message":"no selects allowed"}]}]`))
	assert.NoError(t, err)
	assert.Len(t, dataRules, 1)
	assert.Len(t, dataRules[0].Items, 1)
	assert.Equal(t, "no selects allowed", dataRules[0].Items[0].Message)
}

func TestMatchMessage(t *testing.T) {
	dataRules := []DataRules{
		{Items: []Rule{
			{Type: denyWordListType, Words: []string{"SELECT"}, Message: "deny select message"},
			{Type: patternMatchRegexType, PatternRegex: "[A-Z0-9]+", Message: "pattern message"},
			{Type: denyWordListType, Words: []string{"DROP"}}, // no message configured
		}},
	}

	for _, tt := range []struct {
		msg          string
		ruleType     string
		words        []string
		patternRegex string
		expected     string
	}{
		{
			msg:      "matches deny word rule message",
			ruleType: denyWordListType,
			words:    []string{"SELECT"},
			expected: "deny select message",
		},
		{
			msg:          "matches pattern rule message",
			ruleType:     patternMatchRegexType,
			patternRegex: "[A-Z0-9]+",
			expected:     "pattern message",
		},
		{
			msg:      "returns empty when matched rule has no message",
			ruleType: denyWordListType,
			words:    []string{"DROP"},
			expected: "",
		},
		{
			msg:      "returns empty when words differ",
			ruleType: denyWordListType,
			words:    []string{"UPDATE"},
			expected: "",
		},
		{
			msg:          "returns empty when pattern differs",
			ruleType:     patternMatchRegexType,
			patternRegex: "different",
			expected:     "",
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			assert.Equal(t, tt.expected, MatchMessage(dataRules, tt.ruleType, tt.words, tt.patternRegex))
		})
	}
}
