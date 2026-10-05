package analyzer

import (
	"errors"
	"fmt"
	"math"
)

// Sampling is the optional sampling configuration a provider sends with
// every classification. A nil field is not sent and the model uses its own
// default, so a config that sets none sends the body it always did.
//
// Pointers, because zero is a value: temperature 0 is the most
// deterministic setting, not an absent one.
//
// Not every API has every field. Anthropic has no seed and OpenAI has no
// top_k; their providers refuse those at construction (Unsupported). Gemini
// 3 and later models accept all four and ignore temperature, top_p and top_k
// (Google's model guides say so), which no check here can see.
type Sampling struct {
	Temperature *float64
	TopP        *float64
	TopK        *int
	Seed        *int64
}

// IsZero reports whether no parameter is set.
func (s Sampling) IsZero() bool {
	return s.Temperature == nil && s.TopP == nil && s.TopK == nil && s.Seed == nil
}

// Validate checks the ranges every provider shares. A provider can narrow
// them further: Anthropic's temperature stops at 1, and that answer comes
// from the API.
func (s Sampling) Validate() error {
	var errs []error
	if t := s.Temperature; t != nil && (math.IsNaN(*t) || *t < 0 || *t > 2) {
		errs = append(errs, fmt.Errorf("temperature %v is outside [0, 2]", *t))
	}
	if p := s.TopP; p != nil && (math.IsNaN(*p) || *p < 0 || *p > 1) {
		errs = append(errs, fmt.Errorf("top_p %v is outside [0, 1]", *p))
	}
	if k := s.TopK; k != nil && *k < 1 {
		errs = append(errs, fmt.Errorf("top_k %d is below 1", *k))
	}
	return errors.Join(errs...)
}

// Unsupported returns an error naming every set parameter in fields, for a
// provider whose API does not take them. provider prefixes the message.
func (s Sampling) Unsupported(provider string, fields ...string) error {
	var set []string
	for _, f := range fields {
		switch {
		case f == "temperature" && s.Temperature != nil,
			f == "top_p" && s.TopP != nil,
			f == "top_k" && s.TopK != nil,
			f == "seed" && s.Seed != nil:
			set = append(set, f)
		}
	}
	if len(set) == 0 {
		return nil
	}
	return fmt.Errorf("%s: the API has no %v parameter; remove it from the analyzer section", provider, set)
}
