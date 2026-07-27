package rdp

import (
	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/gateway/rdp/parser"
)

// InitParser compiles the RDP bitmap parser WASM module once at startup so the
// first RDP session does not pay the compile cost and a broken module is
// detected early. It does NOT create a shared parser instance: each RDP session
// creates its own isolated parser (the WASM module keeps per-instance mutable
// state that is unsafe to share across concurrent sessions).
//
// It should be called once at application startup.
func InitParser() error {
	if err := parser.Warmup(); err != nil {
		log.Errorf("failed to initialize RDP parser: %v", err)
		return err
	}
	log.Debugf("RDP bitmap parser compiled successfully")
	return nil
}
