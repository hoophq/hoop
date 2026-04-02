package redactor

import "fmt"

type DataMaskingEntityData struct {
	SupportedEntityTypes []SupportedEntityTypesEntry `json:"supported_entity_types"`
}

type SupportedEntityTypesEntry struct {
	EntityTypes []string `json:"entity_types"`
}

// GuardRailsInfo represents information about a guardrails rule match
type GuardRailsInfo struct {
	RuleName     string              `json:"rule_name"`
	Rule         GuardRailMatchedRule `json:"rule"`
	Direction    string              `json:"direction"`
	MatchedWords []string            `json:"matched_words"`
}

// GuardRailMatchedRule represents the specific internal rule entry that triggered the match
type GuardRailMatchedRule struct {
	Type         string   `json:"type"`
	Words        []string `json:"words,omitempty"`
	PatternRegex string   `json:"pattern_regex,omitempty"`
}

// ErrGuardrailsValidation is an error type for guardrails validation failures
type ErrGuardrailsValidation struct {
	info []GuardRailsInfo
}

// NewErrGuardrailsValidation creates a new ErrGuardrailsValidation
func NewErrGuardrailsValidation(info []GuardRailsInfo) *ErrGuardrailsValidation {
	return &ErrGuardrailsValidation{info: info}
}

// Error implements the error interface
func (e *ErrGuardrailsValidation) Error() string {
	if len(e.info) == 0 {
		return "guardrails validation failed"
	}
	return fmt.Sprintf("Blocked by the following Hoop Guardrails Rules: %s", e.info[0].RuleName)
}

// Info returns the guardrails info
func (e *ErrGuardrailsValidation) Info() []GuardRailsInfo {
	return e.info
}

// FormattedMessage returns a formatted error message
func (e *ErrGuardrailsValidation) FormattedMessage() string {
	if len(e.info) == 0 {
		return "guardrails validation failed"
	}
	return fmt.Sprintf("Blocked by the following Hoop Guardrails Rules: %s", e.info[0].RuleName)
}
