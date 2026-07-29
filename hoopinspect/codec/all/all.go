// Package all registers every codec hoopinspect ships.
//
// Import it for its side effects when a binary should speak every supported
// protocol:
//
//	import _ "github.com/hoophq/hoopinspect/codec/all"
//
// Do NOT import it in a size-sensitive build. An Envoy WASM filter that only
// inspects Postgres should import codec/postgres alone, so the HTTP and
// GraphQL machinery is never linked in.
package all

import (
	_ "github.com/hoophq/hoopinspect/codec/http"
	_ "github.com/hoophq/hoopinspect/codec/postgres"
)
