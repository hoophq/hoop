package redactortypes

import "fmt"

type GuardRailsInfo struct {
	RuleName     string               `json:"rule_name"`
	Rule         GuardRailMatchedRule `json:"rule"`
	Direction    string               `json:"direction"`
	MatchedWords []string             `json:"matched_words"`
}

type GuardRailMatchedRule struct {
	Type         string   `json:"type"`
	Words        []string `json:"words,omitempty"`
	PatternRegex string   `json:"pattern_regex,omitempty"`
}

type ErrGuardrailsValidation struct {
	Err            error
	GuardRailsInfo []GuardRailsInfo `json:"guardrails_info,omitempty"`
}

func NewErrGuardrailsValidation(err error) *ErrGuardrailsValidation {
	return &ErrGuardrailsValidation{Err: err}
}

func (e *ErrGuardrailsValidation) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "guardrails validation failed"
}

func (e *ErrGuardrailsValidation) Unwrap() error {
	return e.Err
}

func (e *ErrGuardrailsValidation) Info() []GuardRailsInfo {
	if len(e.GuardRailsInfo) == 0 {
		return nil
	}
	return e.GuardRailsInfo
}

func (e *ErrGuardrailsValidation) FormattedMessage() string {
	items := e.Info()
	if len(items) == 0 {
		return e.Error()
	}
	return fmt.Sprintf("Blocked by the following Hoop Guardrails Rules: %s", items[0].RuleName)
}
