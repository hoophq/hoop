package apiutils

import "strings"

func NormalizeUserAgent(headerGetterFn func(key string) []string) (ua string) {
	if ua = normalizeUserAgent(headerGetterFn); ua != "" {
		parts := strings.Split(ua, " ")
		// product01/<version> product02/<version>
		if len(parts) > 1 {
			ua = parts[0]
		}
		return strings.Split(ua, "/")[0]
	}
	return
}

func normalizeUserAgent(headerGetterFn func(key string) []string) string {
	data := headerGetterFn("User-Client")
	if len(data) > 0 {
		return data[0]
	}
	data = headerGetterFn("User-Agent")
	if len(data) > 0 {
		return data[0]
	}
	return ""
}
