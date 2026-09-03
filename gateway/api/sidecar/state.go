package apisidecar

import (
	"time"

	"github.com/hoophq/hoop/common/memory"
)

// runtimeStore holds what a sidecar reported about itself at its last call.
// It is process-local on purpose: nothing here is worth a column, a gateway
// restart forgets it, and the next handshake refills it. A multi-replica
// gateway answers from whichever replica the sidecar last reached.
var runtimeStore = memory.New()

type runtimeState struct {
	Version  string
	LastSeen time.Time
}

// recordRuntime overwrites the entry with what a handshake reported.
func recordRuntime(sidecarID, version string) {
	runtimeStore.Set(sidecarID, runtimeState{Version: version, LastSeen: time.Now().UTC()})
}

// loadRuntime returns nil when the sidecar has not called this process.
func loadRuntime(sidecarID string) *runtimeState {
	obj := runtimeStore.Get(sidecarID)
	if obj == nil {
		return nil
	}
	state, ok := obj.(runtimeState)
	if !ok {
		return nil
	}
	return &state
}

// forgetRuntime drops the entry when the sidecar is deleted, so a new sidecar
// reusing the name cannot inherit a stale version.
func forgetRuntime(sidecarID string) { runtimeStore.Del(sidecarID) }
