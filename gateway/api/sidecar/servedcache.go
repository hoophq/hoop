package apisidecar

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"github.com/hoophq/hoop/gateway/models"
	"github.com/hoophq/hoop/gateway/services"
	"github.com/hoophq/hoop/sidecar/daemon"
	"github.com/hoophq/hoop/sidecar/license"
)

// servedDocument is one handshake answer: the bytes the sidecar receives and
// the revision that names them. The revision is the hash of the bytes, as
// configRevision always computed it; the bytes are kept so a steady-state
// handshake writes them instead of composing, checking and encoding the
// document again.
type servedDocument struct {
	body     []byte
	revision string
}

// servedDocuments is the last document served to each sidecar, by sidecar
// id, with the fingerprint of what it was built from. It is never
// invalidated: every handshake recomputes the fingerprint from what it
// already loaded (the stored document's hash from the row, the bound rules,
// the license and its verdict, the build's version and capabilities) and
// takes the entry only when it matches. A rule edit, a config write, a
// license renewal or a lapse, an upgraded sidecar: each changes an input,
// so the next handshake misses and composes. Per replica, so two gateways
// agree by construction, through the fingerprint.
//
// Bounded by clearing: a fleet larger than servedDocumentsMax starts over,
// which costs it one composition each rather than memory without end.
var servedDocuments = struct {
	sync.Mutex
	m map[string]servedEntry
}{m: map[string]servedEntry{}}

const servedDocumentsMax = 50_000

type servedEntry struct {
	fingerprint [32]byte
	servedDocument
}

// servedFingerprint names every input the served document is a function of.
// An empty configuration hash is a row read by a query that did not compute
// it; the caller must not consult the cache with that, see cachedServed.
func servedFingerprint(sc *models.Sidecar, bound models.SidecarBoundRules, library services.RuleLibraryRows, licenseData json.RawMessage, state license.State, hs daemon.Handshake) [32]byte {
	h := sha256.New()
	write := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	write(sc.ConfigurationHash)
	for _, rules := range [][]models.BoundRule{bound.Guardrails, bound.Masking, bound.Analyzers} {
		write("")
		for _, r := range rules {
			write(r.ListenerName)
			write(r.RuleName)
			write(string(r.Spec))
		}
	}
	// The organization library rows the levels select (rule_library).
	for _, rows := range library {
		write("")
		for _, r := range rows {
			write(r.Name)
			write(string(r.Spec))
		}
	}
	write(string(licenseData))
	write(string(state))
	write(hs.Version)
	caps := append([]string(nil), hs.Capabilities...)
	sort.Strings(caps)
	write(strings.Join(caps, ","))
	var out [32]byte
	h.Sum(out[:0])
	return out
}

// cachedServed returns the document served to this sidecar last time when
// it was built from the same inputs.
func cachedServed(sc *models.Sidecar, fingerprint [32]byte) (servedDocument, bool) {
	if sc.ConfigurationHash == "" {
		return servedDocument{}, false
	}
	servedDocuments.Lock()
	defer servedDocuments.Unlock()
	e, ok := servedDocuments.m[sc.ID]
	if !ok || e.fingerprint != fingerprint {
		return servedDocument{}, false
	}
	return e.servedDocument, true
}

func rememberServed(sc *models.Sidecar, fingerprint [32]byte, doc servedDocument) {
	if sc.ConfigurationHash == "" {
		return
	}
	servedDocuments.Lock()
	defer servedDocuments.Unlock()
	if len(servedDocuments.m) >= servedDocumentsMax {
		servedDocuments.m = map[string]servedEntry{}
	}
	servedDocuments.m[sc.ID] = servedEntry{fingerprint: fingerprint, servedDocument: doc}
}
