// Package trust holds the public key licenses are verified against.
//
// The key lives here rather than in license so it has exactly one home and no
// exported setter reachable from the tree at large: internal/ limits importers
// to sidecar/license and sidecar/license/licensetest, which are the verifier
// and the helper that lets a test sign something the verifier accepts.
package trust

import "sync"

// openssl rsa -in ./license.key -pubout
var production = []byte(`
-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAuMaf59LDDC5t06jYtXJB
xDM3+e1POErhDzV1KcATYN0PS39yeqZ4VYxOr/0b8iqoPmYfReoj1GBiXKkMrO5D
BOCCFwSUGnEAPVBUsGhcbtPmEW8iJvMCdiG35GpWgBbn8Q5TAMdEweGQSBo0CPRz
xaOLeCgMv5qx10KpnP/8SRaDmM0vvOksRwJAMmwMaSkQEKOrs97jkDgnBY1mz1TI
zmo40K3nFT6WHgqETIrl3t/fC1Fv25MDrPLE4M3htqBKLKDR99pPHX0gxB3dvwi6
p8mG+hifq6xb6bTDH7ilIhFf30v+jjSfLyZUl56xitSiqF92uJTOZ5Q9xqISo7Sq
yQIDAQAB
-----END PUBLIC KEY-----
`)

var (
	mu  sync.RWMutex
	key = production
)

// Key is the PEM a signature must verify against.
func Key() []byte {
	mu.RLock()
	defer mu.RUnlock()
	return key
}

// Swap installs a different trusted key and returns a function restoring the
// production one.
//
// The trust root is process-wide, so a test that swaps it must not run beside
// one that reads it. licensetest is the only caller and it restores through
// t.Cleanup.
func Swap(pem []byte) func() {
	mu.Lock()
	defer mu.Unlock()
	previous := key
	key = pem
	return func() {
		mu.Lock()
		defer mu.Unlock()
		key = previous
	}
}
