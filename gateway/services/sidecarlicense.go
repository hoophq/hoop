package services

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"sync"
	"time"

	commonlicense "github.com/hoophq/hoop/common/license"
	"github.com/hoophq/hoop/gateway/appconfig"
	"github.com/hoophq/hoop/sidecar/license"
)

// Every sidecar request checks the organization's license twice: the seat
// gate parses it with common/license and the rule caps load it with
// sidecar/license. Each is an RSA signature check over the same bytes, and a
// fleet of thousands of sidecars runs them many times a second. The verdict
// of a document never changes -- only the clock around it -- so the signature
// is checked once per distinct document and the term is read on every call.
//
// Keyed by the document's hash, so a renewed license is a new entry and the
// old one is never consulted again. The map grows by one entry per license
// an organization ever installed, which is the number of renewals.

var (
	sidecarLicenses sync.Map // [32]byte -> license.Status
	seatLicenses    sync.Map // [32]byte -> seatLicense
)

// seatLicense is what OrgHasEnterpriseSidecars learned about one document
// apart from the clock: the parsed license, and whether its signature and
// allowed hosts checked out.
type seatLicense struct {
	license  *commonlicense.License
	verified bool
}

// loadSidecarLicense is license.Load over the organization document, cached
// by content. The Status it returns stores only that the signature checked
// out; its State reads the clock on every use, so caching it is not caching
// a verdict (sidecar/CLAUDE.md, "The license verdict is a function of the
// clock").
//
// A Status carrying an error is not cached. Load reports a document read
// before its issued_at as an error, and that error clears when the time
// arrives; caching it would pin the free-tier caps until a restart. Such a
// document is verified on every call, the cost before this cache existed.
func loadSidecarLicense(licenseData []byte) license.Status {
	key := sha256.Sum256(licenseData)
	if s, ok := sidecarLicenses.Load(key); ok {
		return s.(license.Status)
	}
	s := license.Load(license.Ref{
		Value:  string(licenseData),
		Source: "the organization license",
	})
	if s.Err == nil {
		sidecarLicenses.Store(key, s)
	}
	return s
}

// SidecarLicenseState is the organization license's verdict now, as the
// sidecar's caps read it: the one clock-dependent input to the document the
// handshake serves, so a cached document is keyed on it and a license that
// lapses is re-checked on the next handshake.
func SidecarLicenseState(licenseData []byte) license.State {
	if len(bytes.TrimSpace(licenseData)) == 0 {
		return license.Status{}.State()
	}
	return loadSidecarLicense(licenseData).State()
}

// isEnterpriseLicense reports whether the document is a verified Enterprise
// license inside its term now. Same answer commonlicense.Parse gives, with the
// signature checked once per document.
func isEnterpriseLicense(licenseData []byte) bool {
	key := sha256.Sum256(licenseData)
	var seat seatLicense
	if cached, ok := seatLicenses.Load(key); ok {
		seat = cached.(seatLicense)
	} else {
		seat = parseSeatLicense(licenseData)
		seatLicenses.Store(key, seat)
	}
	return seat.verified &&
		seat.license.VerifyTerm(time.Now().UTC()) == nil &&
		seat.license.Payload.Type == commonlicense.EnterpriseType
}

func parseSeatLicense(licenseData []byte) seatLicense {
	host := appconfig.Get().ApiHostname()
	l, err := commonlicense.Parse(licenseData, host)
	switch {
	case err == nil:
		return seatLicense{license: l, verified: true}
	case errors.Is(err, commonlicense.ErrExpired), errors.Is(err, commonlicense.ErrUsedBeforeIssued):
		// The signature checked out and only the term failed, which the
		// caller re-reads. Parse stops before the host check on a term
		// error, so it runs here.
		return seatLicense{license: l, verified: l.VerifyHost(host) == nil}
	default:
		return seatLicense{}
	}
}
