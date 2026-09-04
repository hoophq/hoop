// Package mongodb registers the MongoDB codec with the inspect registry.
//
// The codec itself lives in github.com/hoophq/libhoop/v2/codec/mongodb. This
// package is the seam between the repositories: libhoop is a leaf and cannot
// import sidecar to register itself.
package mongodb

import (
	"github.com/hoophq/hoop/sidecar/inspect"
	codecmongodb "github.com/hoophq/libhoop/v2/codec/mongodb"
)

// New builds one stateful MongoDB codec per connection.
func New() inspect.Codec { return codecmongodb.New() }

func init() {
	inspect.Register(func() inspect.Codec { return New() })
}
