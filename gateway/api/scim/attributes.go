package apiscim

import (
	"strings"

	"github.com/elimity-com/scim"
)

// The library validates attributes against the schema and hands them over as
// decoded JSON. These helpers read that shape without trusting it further.

// attr looks a key up ignoring case: SCIM attribute names are case
// insensitive, and clients disagree on the spelling.
func attr(attrs map[string]any, key string) (any, bool) {
	if v, ok := attrs[key]; ok {
		return v, true
	}
	for k, v := range attrs {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return nil, false
}

func stringAttr(attrs map[string]any, key string) string {
	v, _ := attr(attrs, key)
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// boolValue reads a boolean, including the "True"/"False" strings Entra ID
// sends.
func boolValue(v any, fallback bool) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		switch strings.ToLower(strings.TrimSpace(b)) {
		case "true":
			return true
		case "false":
			return false
		}
	}
	return fallback
}

func mapAttr(attrs map[string]any, key string) map[string]any {
	v, _ := attr(attrs, key)
	m, _ := v.(map[string]any)
	return m
}

func listAttr(attrs map[string]any, key string) []map[string]any {
	v, _ := attr(attrs, key)
	return listValue(v)
}

func listValue(v any) []map[string]any {
	items, _ := v.([]any)
	var out []map[string]any
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

// primaryEmail picks the primary email, else the work email, else the first.
func primaryEmail(emails []map[string]any) string {
	var work, first string
	for _, e := range emails {
		value := stringAttr(e, "value")
		if value == "" {
			continue
		}
		if boolValue(e["primary"], false) {
			return value
		}
		if work == "" && strings.EqualFold(stringAttr(e, "type"), "work") {
			work = value
		}
		if first == "" {
			first = value
		}
	}
	if work != "" {
		return work
	}
	return first
}

// displayName is what hoop shows for a user: the display name, else the
// formatted name, else the given and family names.
func displayName(attrs map[string]any) string {
	if n := stringAttr(attrs, "displayName"); n != "" {
		return n
	}
	name := mapAttr(attrs, "name")
	if name == nil {
		return ""
	}
	if n := stringAttr(name, "formatted"); n != "" {
		return n
	}
	return strings.TrimSpace(stringAttr(name, "givenName") + " " + stringAttr(name, "familyName"))
}

// memberIDs reads the "value" of each member, the id hoop gave the user.
func memberIDs(members []map[string]any) []string {
	var ids []string
	for _, m := range members {
		if id := stringAttr(m, "value"); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func externalID(attrs scim.ResourceAttributes) string {
	return stringAttr(attrs, "externalId")
}
