package apivalidation

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// MaxTags is the maximum number of key/value tags of one resource.
const MaxTags = 10

var (
	reTagKey   = regexp.MustCompile(`^[a-zA-Z0-9_]+(?:[-\./]?[a-zA-Z0-9_]+){0,}$`)
	reTagValue = regexp.MustCompile(`^[a-zA-Z0-9-_\+=@\/:\s]+$`)
)

// TagErrors returns one message for each invalid key or value of tags, in key
// order. field is the request attribute that holds the tags.
func TagErrors(field string, tags map[string]string) []string {
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var errs []string
	for _, key := range keys {
		val := tags[key]
		if len(key) < 1 || len(key) > 64 || !reTagKey.MatchString(key) {
			errs = append(errs,
				fmt.Sprintf("%s (%v), keys must contain between 1 and 64 alphanumeric characters, ", field, key)+
					"it may include (-), (_), (/), or (.) characters and it must not end with (-), (/) or (-)")
		}
		if len(val) < 1 || len(val) > 256 || !reTagValue.MatchString(val) {
			errs = append(errs, fmt.Sprintf("%s (%v), values must contain between 1 and 256 alphanumeric characters, ", field, key)+
				"it may include space, (-), (_), (/), (+), (@), (:), (=) or (.) characters")
		}
	}
	return errs
}

// ValidateTags applies MaxTags and TagErrors to tags.
func ValidateTags(field string, tags map[string]string) error {
	if len(tags) > MaxTags {
		return fmt.Errorf("max tag association reached (%d)", MaxTags)
	}
	if errs := TagErrors(field, tags); len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}
