package http

import (
	"path"
	"strings"

	"github.com/hoophq/hoop/sidecar/inspect"
)

// cleanResource resolves the resource as an upstream that cleans paths does.
// An upstream may also route the raw path, so a dot segment keeps that form too.
func cleanResource(stmt *inspect.Statement) {
	d := stmt.HTTP
	if d == nil || !strings.HasPrefix(d.Resource, "/") {
		return
	}
	slashed := strings.ReplaceAll(d.Resource, `\`, "/")
	clean := path.Clean(slashed)
	if strings.HasSuffix(slashed, "/") && clean != "/" {
		clean += "/"
	}
	if clean == d.Resource {
		return
	}
	if hasDotSegment(slashed) {
		if stmt.Metadata == nil {
			stmt.Metadata = map[string]string{}
		}
		stmt.Metadata[inspect.MetadataHTTPResourceUnresolved] = slashed
	}
	d.Resource = clean
	stmt.Tables = []string{strings.ToLower(clean)}
}

func hasDotSegment(p string) bool {
	for seg := range strings.SplitSeq(p, "/") {
		if seg == "." || seg == ".." {
			return true
		}
	}
	return false
}
