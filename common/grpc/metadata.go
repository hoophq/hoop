package grpc

import (
	"strings"

	"google.golang.org/grpc/metadata"
)

// MetaGet returns the key from metadata or an empty string
func MetaGet(md metadata.MD, metaName string) string {
	data := md.Get(metaName)
	if len(data) == 0 {
		// keeps compatibility with old clients that
		// pass headers with underline. HTTP headers are not
		// accepted with underline for some servers, e.g.: nginx
		data = md.Get(strings.ReplaceAll(metaName, "-", "_"))
		if len(data) == 0 {
			return ""
		}
	}
	return data[0]
}
