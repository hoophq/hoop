// Package all registers every codec hoopinspect can drive.
//
// Import it for its side effects when a binary should speak every supported
// protocol:
//
//	import _ "github.com/hoophq/hoop/hoopinspect/codec/all"
//
// Import a single codec instead when a binary only ever speaks one protocol:
// a listener fronting Postgres should import codec/postgres alone, so the
// HTTP and TDS machinery is never linked in.
//
// The decoders themselves live in github.com/hoophq/libhoop/v2/codec/*. The
// packages under this one are the registration seam — libhoop is a leaf and
// cannot name hoopinspect, so it cannot register anything itself.
package all

import (
	_ "github.com/hoophq/hoop/hoopinspect/codec/http"
	_ "github.com/hoophq/hoop/hoopinspect/codec/mssql"
	_ "github.com/hoophq/hoop/hoopinspect/codec/postgres"
)
