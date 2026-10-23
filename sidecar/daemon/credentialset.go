package daemon

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"sync/atomic"
	"time"

	codectypes "github.com/hoophq/libhoop/v2/codec/types"
)

// credentialSet is the hoop credentials the plane minted for this sidecar,
// indexed for the hoop_credential verifier. One per process: Run creates it
// from the startup document and the reloader swaps the whole set on every
// heartbeat that carries one, so a key minted or revoked on the plane is in
// force at the next login without a restart. Reads take no lock.
type credentialSet struct {
	cur atomic.Pointer[credentialIndex]
}

type credentialIndex struct {
	// byLane keeps each lane's entries in served order, with the hash
	// decoded once so a login compares bytes, not hex.
	byLane map[string][]indexedCredential
}

type indexedCredential struct {
	CredentialSetEntry
	hash [sha256.Size]byte
}

func newCredentialSet(entries []CredentialSetEntry) *credentialSet {
	s := &credentialSet{}
	s.swap(entries)
	return s
}

// swap replaces the set. An entry whose hash is not 32 hex bytes is dropped:
// it can match nothing, and keeping it would let a malformed row shadow
// nothing but confuse the constant-time walk.
func (s *credentialSet) swap(entries []CredentialSetEntry) {
	idx := &credentialIndex{byLane: map[string][]indexedCredential{}}
	for _, e := range entries {
		raw, err := hex.DecodeString(e.Hash)
		if err != nil || len(raw) != sha256.Size {
			continue
		}
		ic := indexedCredential{CredentialSetEntry: e}
		copy(ic.hash[:], raw)
		idx.byLane[e.Lane] = append(idx.byLane[e.Lane], ic)
	}
	s.cur.Store(idx)
}

// size is the number of entries in force, for a log line.
func (s *credentialSet) size() int {
	n := 0
	for _, l := range s.cur.Load().byLane {
		n += len(l)
	}
	return n
}

// match finds the lane's entry whose hash is sha256(secret). Every entry of
// the lane is compared, in constant time, with no early exit, so neither the
// timing of a refusal nor the entry's position tells a caller how close a
// guess came. Expiry is the caller's check: a match on an expired key is a
// different refusal from an unknown one.
func (s *credentialSet) match(lane, secret string) (CredentialSetEntry, bool) {
	sum := sha256.Sum256([]byte(secret))
	return s.byDigest(lane, sum[:])
}

// byHash finds the lane's entry with this hex hash: the form a driver passes
// as "scram:<hash>" after a SCRAM proof against the entry's material, where
// the raw key is never seen.
func (s *credentialSet) byHash(lane, hexHash string) (CredentialSetEntry, bool) {
	raw, err := hex.DecodeString(hexHash)
	if err != nil || len(raw) != sha256.Size {
		return CredentialSetEntry{}, false
	}
	return s.byDigest(lane, raw)
}

func (s *credentialSet) byDigest(lane string, digest []byte) (CredentialSetEntry, bool) {
	var found CredentialSetEntry
	hit := 0
	for _, e := range s.cur.Load().byLane[lane] {
		if subtle.ConstantTimeCompare(e.hash[:], digest) == 1 {
			found, hit = e.CredentialSetEntry, 1
		}
	}
	return found, hit == 1
}

// scram returns the SCRAM material of the lane's credential whose Subject or
// Email is user, for a driver that must act as the SCRAM server on the
// client leg. An expired entry has none: the driver then refuses before any
// proof. With two live keys for one user the newest expiry wins, which is
// the one just minted during a rotation.
func (s *credentialSet) scram(lane, user string) (codectypes.SCRAMMaterial, bool) {
	var pick *indexedCredential
	for i := range s.cur.Load().byLane[lane] {
		e := &s.cur.Load().byLane[lane][i]
		if e.SCRAM == nil || e.expired(time.Now()) || (e.Subject != user && e.Email != user) {
			continue
		}
		if pick == nil || e.ExpiresAt.After(pick.ExpiresAt) {
			pick = e
		}
	}
	if pick == nil {
		return codectypes.SCRAMMaterial{}, false
	}
	m, ok := pick.SCRAM.material()
	if !ok {
		return codectypes.SCRAMMaterial{}, false
	}
	m.Hash = pick.Hash
	return m, true
}

// expired reports whether the entry is past its term at now. An entry the
// plane served with no term never expires here; the plane decides its life.
func (e CredentialSetEntry) expired(now time.Time) bool {
	return !e.ExpiresAt.IsZero() && !now.Before(e.ExpiresAt)
}

// material decodes the served base64 fields into the codec's form. A field
// that does not decode makes the whole material unusable, so a driver
// refuses rather than proving against garbage.
func (m *SCRAMMaterial) material() (codectypes.SCRAMMaterial, bool) {
	salt, err1 := base64.StdEncoding.DecodeString(m.Salt)
	stored, err2 := base64.StdEncoding.DecodeString(m.StoredKey)
	server, err3 := base64.StdEncoding.DecodeString(m.ServerKey)
	if err1 != nil || err2 != nil || err3 != nil || m.Iterations <= 0 || len(stored) == 0 || len(server) == 0 {
		return codectypes.SCRAMMaterial{}, false
	}
	return codectypes.SCRAMMaterial{Salt: salt, Iterations: m.Iterations, StoredKey: stored, ServerKey: server}, true
}
