package httputils

import (
	"net/url"
	"strings"
)

// WithQueryParam adds key=value to rawURL and keeps the query it already has
// (the web app's callback can carry ?next=). A URL that does not parse gets
// the parameter joined with the right separator.
func WithQueryParam(rawURL, key, value string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		sep := "?"
		if strings.Contains(rawURL, "?") {
			sep = "&"
		}
		return rawURL + sep + url.QueryEscape(key) + "=" + url.QueryEscape(value)
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String()
}
