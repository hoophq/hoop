package guardrails

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
	streamDirection string
	ruleType        string
	words           []string
	patternRegex    string
}

func (e ErrRuleMatch) Error() string {
	switch e.ruleType {
	case denyWordListType:
		return fmt.Sprintf("validation error, match guard rails %v rule, type=%v, words=%v",
			e.streamDirection, e.ruleType, e.words)
	case patternMatchRegexType:
		return fmt.Sprintf("validation error, match guard rails %v rule, type=%v, pattern=%v",
			e.streamDirection, e.ruleType, e.patternRegex)
	}
	return fmt.Sprintf("validation error, match guard rails %v rule, type=%v", e.streamDirection, e.ruleType)
}

type DataRules struct {
	Items []Rule `json:"rules"`
}

type Rule struct {
	Type         string   `json:"type"`
	Words        []string `json:"words"`
	PatternRegex string   `json:"pattern_regex"`
}

func (r *Rule) validate(streamDirection string, data []byte) error {
	switch r.Type {
	case denyWordListType:
		for _, word := range r.Words {
			// skip empty rules
			if word == "" {
				continue
			}
			if !strings.Contains(string(data), word) {
				continue
			}
			return &ErrRuleMatch{streamDirection: streamDirection, ruleType: r.Type, words: r.Words}
		}
	case patternMatchRegexType:
		// skip empty regex
		if r.PatternRegex == "" {
			return nil
		}
		regex, err := regexp.Compile(r.PatternRegex)
		if err != nil {
			return fmt.Errorf("failed parsing regex, reason=%v", err)
		}
		if regex.Match(data) {
			return &ErrRuleMatch{streamDirection: streamDirection, ruleType: r.Type, patternRegex: r.PatternRegex}
		}
	default:
		return fmt.Errorf("unknown rule type %q", r.Type)
	}
	return nil
}

func Decode(data []byte) ([]DataRules, error) {
	var dataRules []DataRules
	if err := json.Unmarshal(data, &dataRules); err != nil {
		return dataRules, fmt.Errorf("unable to decode rules, reason=%v", err)
	}
	return dataRules, nil
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
