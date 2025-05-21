package runbooks

import (
	"encoding/base64"
	"fmt"
	"regexp"
	ttemplate "text/template"
)

var defaultInputTypes = []string{"text", "number", "tel", "time", "date", "url", "email", "select", "textarea", "password"}

type templateFnHandler struct {
	items map[string]string
}

func (h *templateFnHandler) AsEnv(envKey, envVal string) string {
	h.items[fmt.Sprintf("envvar:%s", envKey)] = base64.StdEncoding.EncodeToString([]byte(envVal))
	return ""
}

func defaultStaticTemplateFuncs() ttemplate.FuncMap {
	return ttemplate.FuncMap{
		"required": func(msg, s string) (string, error) {
			if s == "" {
				return "", fmt.Errorf(msg)
			}
			return s, nil
		},
		"default": func(d, s string) string {
			if s == "" {
				return d
			}
			return s
		},
		"pattern": func(p, s string) (string, error) {
			ok, err := regexp.Match(p, []byte(s))
			if err != nil {
				return "", fmt.Errorf("regexp error: %v", err)
			}
			if ok {
				return s, nil
			}
			return "", fmt.Errorf("pattern didn't match:%s", p)
		},
		"placeholder": func(_, s string) string { return s },
		"options": func(v ...string) string {
			if len(v) == 0 {
				return ""
			}
			return v[len(v)-1]
		},
		"description": func(_, s string) string { return s },
		"type":        func(_, s string) string { return s },
		"squote":      func(s string) string { return fmt.Sprintf(`'%s'`, s) },
		"dquote":      func(s string) string { return fmt.Sprintf(`"%s"`, s) },
		"quotechar":   func(c, s string) string { return fmt.Sprintf(`%s%s%s`, c, s, c) },
		"encodeb64":   func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) },
		"decodeb64": func(s string) (string, error) {
			dec, err := base64.StdEncoding.DecodeString(s)
			return string(dec), err
		},
	}
}
