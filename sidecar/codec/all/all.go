// Package all registers every codec sidecar can drive.
//
// Import it for its side effects when a binary should speak every supported
// protocol:
//
//	import _ "github.com/hoophq/hoop/sidecar/codec/all"
//
// Import a single codec instead when a binary only ever speaks one protocol:
// a listener fronting Postgres should import codec/postgres alone, so the
// HTTP and TDS machinery is never linked in.
//
// The decoders themselves live in github.com/hoophq/libhoop/v2/codec/*. The
// packages under this one are the registration seam — libhoop is a leaf and
// cannot name sidecar, so it cannot register anything itself.
package all

import (
	_ "github.com/hoophq/hoop/sidecar/codec/http"
	_ "github.com/hoophq/hoop/sidecar/codec/mongodb"
	_ "github.com/hoophq/hoop/sidecar/codec/mssql"
	_ "github.com/hoophq/hoop/sidecar/codec/mysql"
	_ "github.com/hoophq/hoop/sidecar/codec/postgres"
)
