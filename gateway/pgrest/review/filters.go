package pgreview

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	reSanitize, _       = regexp.Compile(`^[a-zA-Z0-9_]+(?:[-\.]?[a-zA-Z0-9_]+){1,128}$`)
	ErrInvalidOptionVal = errors.New("option values must contain between 1 and 127 alphanumeric characters, it may include (-), (_) or (.) characters")
)

type Option struct {
	key string
	val string
}

var availableOptions = map[string]string{
	"connections": "array",
	"users":       "array",
}

// WithOption specify a key pair of options to apply the filtering.
// Each applied option is considered a logical operator AND
func WithOption(key string, val string) *Option {
	return &Option{key: key, val: val}
}

func urlEncodeOptions(opts []*Option) (string, error) {
	var result string
	for i, opt := range opts {
		optType, found := availableOptions[opt.key]
		if !found {
			continue
		}

		// if val is empty, query for null fields
		val := "is.null"
		if optType == "array" {
			var tagVals []string
			for _, tagVal := range strings.Split(opt.val, ",") {
				tagVal = strings.TrimSpace(tagVal)
				if !reSanitize.MatchString(tagVal) {
					return "", ErrInvalidOptionVal
				}
				tagVals = append(tagVals, tagVal)
			}
			if len(tagVals) > 0 {
				// contains
				val = "cs.{" + strings.Join(tagVals, ",") + "}"
			}
		}
		if i == 0 {
			result = fmt.Sprintf("%s=%s", opt.key, val)
			continue
		}
		result += fmt.Sprintf("&%s=%s", opt.key, val)
	}
	if len(result) > 0 {
		return "&" + result, nil
	}
	return result, nil
}
