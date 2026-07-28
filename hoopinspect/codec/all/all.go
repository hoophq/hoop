// Package all registers every codec hoopinspect ships.
//
// Import it for its side effects when a binary should speak all four
// protocols:
//
//	import _ "github.com/hoophq/hoopinspect/codec/all"
//
// Do NOT import it in a size-sensitive build. An Envoy WASM filter that only
// inspects Postgres should import codec/postgres alone, so the MongoDB BSON
// walker and the TDS decoder are never linked in.
package all

import (
	_ "github.com/hoophq/hoopinspect/codec/http"
	_ "github.com/hoophq/hoopinspect/codec/mongodb"
	_ "github.com/hoophq/hoopinspect/codec/mssql"
	_ "github.com/hoophq/hoopinspect/codec/mysql"
	_ "github.com/hoophq/hoopinspect/codec/postgres"
)
