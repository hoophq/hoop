package sshproxy

import (
	"bytes"
	"fmt"

	"github.com/google/uuid"
	"github.com/hoophq/hoop/gateway/models"
	"golang.org/x/crypto/ssh"
)

// UserMapping configures how a certificate attribute is resolved to a Hoop user.
// CertAttr is the certificate field inspected ("principal" or "key_id").
// UserAttr is the user table column matched against it ("email", "subject", or "user_id").
type UserMapping struct {
	CertAttr string
	UserAttr string
}

// certSession holds per-connection certificate state, established at handshake
// time and consulted on every channel-open and channel request. A nil
// certSession means the connection used password authentication.
//
// Extensions enforced at the gateway proxy layer:
//   - permit-pty               → pty-req channel request
//   - permit-port-forwarding   → direct-tcpip channel open
//   - permit-agent-forwarding  → auth-agent-req@openssh.com channel request
//   - permit-X11-forwarding    → x11-req channel request
//
// Critical options enforced at the gateway proxy layer:
//   - force-command            → exec/shell/subsystem channel requests
//   - source-address           → enforced by ssh.CertChecker.Authenticate at handshake time
//
// permit-user-rc has no corresponding SSH protocol message; it controls
// server-side execution of ~/.ssh/rc and is enforced by the target SSH server,
// not by the proxy layer.
type certSession struct {
	cert         *ssh.Certificate
	matchedValue string // the cert attribute value (principal or key_id) that resolved the user
	userSubject  string // Hoop user subject resolved from the mapping at auth time
	orgID        string // org the user belongs to; used for connection existence checks
}

// allowPortForwarding reports whether TCP port forwarding (direct-tcpip) is
// permitted. The permit-port-forwarding extension must be present.
func (s *certSession) allowPortForwarding() bool {
	_, ok := s.cert.Extensions["permit-port-forwarding"]
	return ok
}

// lookupUserByCert finds the Hoop user that matches the certificate using the
// provided UserMapping. For CertAttr "principal", all ValidPrincipals are
// tried in order and the first match wins. For "key_id", the certificate's
// KeyId field is used directly. Returns the matched user, the cert attribute
// value that produced the match, and any error.
func lookupUserByCert(cert *ssh.Certificate, m UserMapping) (*models.User, string, error) {
	switch m.CertAttr {
	case "key_id":
		user, err := lookupUserByAttr(m.UserAttr, cert.KeyId)
		if err != nil {
			return nil, "", fmt.Errorf("user lookup by key_id=%q failed: %w", cert.KeyId, err)
		}
		if user == nil {
			return nil, "", fmt.Errorf("no user found matching key_id=%q, user-attr=%q", cert.KeyId, m.UserAttr)
		}
		return user, cert.KeyId, nil
	default: // "principal"
		for _, p := range cert.ValidPrincipals {
			user, err := lookupUserByAttr(m.UserAttr, p)
			if err != nil || user == nil {
				continue
			}
			return user, p, nil
		}
		return nil, "", fmt.Errorf("no user found matching any principal, user-attr=%q", m.UserAttr)
	}
}

func lookupUserByAttr(userAttr, value string) (*models.User, error) {
	switch userAttr {
	case "email":
		return models.GetUserByEmail(value)
	case "subject":
		return models.GetUserBySubject(value)
	case "user_id":
		if _, err := uuid.Parse(value); err != nil {
			return nil, nil
		}
		return models.GetUserByID(value)
	default:
		return nil, fmt.Errorf("unknown user_attr %q", userAttr)
	}
}

// buildCertChecker constructs an ssh.CertChecker that trusts any of the
// provided CA public keys. Each entry must be in authorized_keys format
// (e.g. "ssh-ed25519 AAAA..."). Returns nil when trustedCAs is empty.
func buildCertChecker(trustedCAs []string) (*ssh.CertChecker, error) {
	if len(trustedCAs) == 0 {
		return nil, nil
	}
	caKeys := make([]ssh.PublicKey, 0, len(trustedCAs))
	for _, raw := range trustedCAs {
		key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(raw))
		if err != nil {
			return nil, fmt.Errorf("failed parsing trusted CA %q: %w", raw, err)
		}
		caKeys = append(caKeys, key)
	}
	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			authBytes := auth.Marshal()
			for _, ca := range caKeys {
				if bytes.Equal(ca.Marshal(), authBytes) {
					return true
				}
			}
			return false
		},
	}
	return checker, nil
}
