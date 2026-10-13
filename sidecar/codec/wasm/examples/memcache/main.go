// The x-memcache plug-in: the memcached text protocol as a hoop codec
// plug-in, written against the TinyGo SDK. memcache.go holds the decoder;
// this file holds the manifest and the entry point.
//
// Build (see README.md and build.sh):
//
//	tinygo build -o dist/memcache.wasm -target wasm-unknown -opt=z -no-debug \
//	  -tags hoop_deny,hoop_content .
package main

import codec "github.com/hoophq/hoop/sidecar/codec/wasm/sdk/tinygo"

const (
	protocol = "x-memcache"
	version  = "0.1.0"

	// Statement metadata. A metadata rule reads these; the command is the
	// word memcached dispatches on, which operation alone cannot carry.
	commandKey = protocol + ".command"
	keyKey     = protocol + ".key"
	noreplyKey = protocol + ".noreply"
	replyKey   = protocol + ".reply"

	optMaxLineBytes     = "max_line_bytes"
	defaultMaxLineBytes = 2048
)

func init() {
	codec.Serve(func() codec.Codec { return &memcache{} }, codec.Manifest{
		Protocol:     protocol,
		Label:        "Memcached text",
		Version:      version,
		Capabilities: []codec.Capability{codec.CapDeny, codec.CapContent},
		Instances:    "per_connection",
		Options: []codec.OptionSpec{{
			Name: optMaxLineBytes, Label: "Max line bytes", Type: "int", Default: "2048",
			Help: "Longest command or reply line, in bytes, before the connection is dropped as malformed. Data blocks are not lines and have no limit here.",
		}},
	})
}

// main never runs under wasm-unknown; Serve is called from init.
func main() {}
