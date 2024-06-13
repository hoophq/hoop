package pgconnections

import (
	"fmt"
	"net/url"
	"strings"
)

type ConnectionOption struct {
	key []string
	val string
}

var availableOptions = map[string]string{
	"type":       "string",
	"subtype":    "string",
	"managed_by": "string",
	"agent_id":   "string",
	"tags":       "map",
}

// WithOption specify a key pair of options to apply the filtering.
// Each applied option is considered a logical operator AND
func WithOption(optTuple []string, val string) *ConnectionOption {
	return &ConnectionOption{key: optTuple, val: val}
}

func urlEncodeOptions(opts []*ConnectionOption) (v string) {
	for i, opt := range opts {
		if len(opt.key) == 0 || len(opt.key) > 2 {
			continue
		}
		optType, found := availableOptions[opt.key[0]]
		if !found {
			continue
		}
		optKey := opt.key[0]
		val := "is.null"
		if opt.val != "" {
			val = fmt.Sprintf("eq.%s", url.QueryEscape(opt.val))
		}
		if optType == "map" {
			// tags.foo = tags->>foo
			optKey = strings.Join(opt.key, "->>")
		}
		if i == 0 {
			v = fmt.Sprintf("%s=%s", optKey, val)
			continue
		}
		v += fmt.Sprintf("&%s=%s", optKey, val)
	}
	if len(v) > 0 {
		return "&" + v
	}
	return
}
