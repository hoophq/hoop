package openapi

// DLPAnalyzeRequest is the payload for the on-demand PII / guardrail analysis endpoint.
type DLPAnalyzeRequest struct {
	// Items contains the text entries to analyze
	Items []DLPAnalyzeItem `json:"items" binding:"required,min=1,max=50,dive"`
	// Connection, when set, resolves the guardrail rules and data masking
	// entity types associated with this connection instead of the org-wide defaults
	Connection string `json:"connection,omitempty" example:"pgdemo"`
	// EntityTypes overrides the set of entity types used for the PII analysis.
	// When empty, the entity types are resolved from the connection data masking
	// rules (if a connection is provided) or a default set of common PII entities
	EntityTypes []string `json:"entity_types,omitempty" example:"EMAIL_ADDRESS,PERSON"`
}

// DLPAnalyzeItem is a single text entry to analyze.
type DLPAnalyzeItem struct {
	// ID is a caller-provided identifier echoed back in the response
	ID string `json:"id" binding:"required" example:"msg-01"`
	// Text is the content to analyze. Limited to 64KiB per item
	Text string `json:"text" binding:"required,max=65536"`
	// GuardrailDirection, when set, validates this item against the guardrail
	// rules configured for the given direction. Empty skips guardrail validation
	GuardrailDirection string `json:"guardrail_direction,omitempty" binding:"omitempty,oneof=input output" enums:"input,output"`
}

// DLPAnalyzeResponse is the response of the on-demand PII / guardrail analysis endpoint.
type DLPAnalyzeResponse struct {
	Results []DLPAnalyzeResult `json:"results"`
}

// DLPAnalyzeResult contains the analysis outcome for a single item.
type DLPAnalyzeResult struct {
	// ID is the caller-provided item identifier
	ID string `json:"id" example:"msg-01"`
	// Findings aggregates the detected PII entities by type
	Findings []DLPFinding `json:"findings"`
	// Guardrails lists the guardrail rules violated by this item
	Guardrails []DLPGuardrailMatch `json:"guardrails"`
	// Error is set when the analysis of this item failed. Other items
	// in the same batch are processed independently
	Error string `json:"error,omitempty"`
}

// DLPFinding aggregates the occurrences of a detected PII entity type.
type DLPFinding struct {
	EntityType string `json:"entity_type" example:"EMAIL_ADDRESS"`
	Count      int64  `json:"count" example:"3"`
	// Matches locates each occurrence in the submitted text. The matched
	// values themselves are never echoed back: callers hold the original
	// text and can extract them from the offsets
	Matches []DLPFindingMatch `json:"matches,omitempty"`
}

// DLPFindingMatch is the location of a single detected entity occurrence.
// Start and End are character (rune) offsets into the item text, not bytes.
type DLPFindingMatch struct {
	Start int     `json:"start" example:"10"`
	End   int     `json:"end" example:"32"`
	Score float64 `json:"score" example:"0.85"`
}

// DLPGuardrailMatch describes a guardrail rule violation.
type DLPGuardrailMatch struct {
	// RuleName is the name of the violated guardrail rule
	RuleName string `json:"rule_name" example:"no-aws-keys"`
	// Direction is the stream direction of the violated rule (input or output)
	Direction string `json:"direction" enums:"input,output"`
	// RuleType is the type of the violated rule (deny_words_list or pattern_match)
	RuleType string `json:"rule_type" example:"pattern_match"`
	// Words is the configured deny word list of the rule, when applicable
	Words []string `json:"words,omitempty"`
	// PatternRegex is the configured regex pattern of the rule, when applicable
	PatternRegex string `json:"pattern_regex,omitempty"`
	// MatchedWords contains the substrings that triggered the violation
	MatchedWords []string `json:"matched_words,omitempty"`
}
