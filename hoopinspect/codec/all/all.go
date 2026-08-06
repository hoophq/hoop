// Package all registers every codec hoopinspect ships.
//
// Import it for its side effects when a binary should speak every supported
// protocol:
//
//	import _ "github.com/hoophq/hoopinspect/codec/all"
//
// Import a single codec instead when a binary only ever speaks one protocol:
// a listener fronting Postgres should import codec/postgres alone, so the
// HTTP machinery is never linked in.
package all

import (
	_ "github.com/hoophq/hoopinspect/codec/http"
	_ "github.com/hoophq/hoopinspect/codec/postgres"
)
