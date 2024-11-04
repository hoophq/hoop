package transportext

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

const (
	denyWordListType      string = "deny_words_list"
	patternMatchRegexType string = "pattern_match"
)

type ErrRuleMatch struct {
	name            string
	streamDirection string
}

func (e ErrRuleMatch) Error() string {
	if e.name != "" {
		return fmt.Sprintf("validation error, match guard rails %v rule, name=%v", e.streamDirection, e.name)
	}
	return fmt.Sprintf("validation error, match guard rails %v rule", e.streamDirection)
}

type DataRules struct {
	Items []Rule `json:"rules"`
}

type Rule struct {
	Type         string   `json:"type"`
	Words        []string `json:"words"`
	PatternRegex string   `json:"pattern_regex"`
	Name         string   `json:"name"`
}

func (r *Rule) validate(streamDirection string, data []byte) error {
	switch r.Type {
	case denyWordListType:
		for _, word := range r.Words {
			if !strings.Contains(string(data), word) {
				continue
			}
			return &ErrRuleMatch{name: r.Name, streamDirection: streamDirection}
		}
	case patternMatchRegexType:
		regex, err := regexp.Compile(r.PatternRegex)
		if err != nil {
			return fmt.Errorf("failed parsing regex, reason=%v", err)
		}
		if regex.Match(data) {
			return &ErrRuleMatch{name: r.Name, streamDirection: streamDirection}
		}
	default:
		return fmt.Errorf("unknown rule type %q", r.Type)
	}
	return nil
}

func Decode(data []byte) ([]DataRules, error) {
	var root []DataRules
	if err := json.Unmarshal(data, &root); err != nil {
		return root, fmt.Errorf("unable to decode rules, reason=%v", err)
	}
	return root, nil
}

func Validate(streamDirection string, ruleData, data []byte) error {
	if len(ruleData) == 0 {
		return nil
	}
	dataRules, err := Decode(ruleData)
	if err != nil {
		return err
	}
	for _, dataRule := range dataRules {
		for _, rule := range dataRule.Items {
			if err := rule.validate(streamDirection, data); err != nil {
				return err
			}
		}
	}
	return nil
}
