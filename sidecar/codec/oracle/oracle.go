// Package oracle registers the Oracle Net/TTC codec with the sidecar inspector.
// libhoop owns the wire format; this seam supplies the Oracle SQL classifier.
package oracle

import (
	"github.com/hoophq/hoop/sidecar/inspect"
	"github.com/hoophq/hoop/sidecar/lexer"
	codecoracle "github.com/hoophq/libhoop/v2/codec/oracle"
	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

// New constructs a connection-scoped Oracle codec.
func New() inspect.Codec {
	return codecoracle.New(codecoracle.Options{
		Analyze: inspect.AnalyzeSQL,
		Split: func(sql string, _ codectypes.Protocol) []string {
			return lexer.Split(sql, lexer.Oracle)
		},
	})
}

func init() {
	inspect.Register(func() inspect.Codec { return New() })
}
