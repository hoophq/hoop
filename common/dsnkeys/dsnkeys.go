package dsnkeys

import (
	"crypto/sha256"
	"fmt"
	"net/url"

	pb "github.com/hoophq/hoop/common/proto"
)

var (
	ErrEmpty             = fmt.Errorf("dsn is empty")
	ErrInvalidMode       = fmt.Errorf("invalid agent mode")
	ErrSecretKeyNotFound = fmt.Errorf("secret key not found in dsn")
)

// DSN represents a parsed url in the format below
//
// format: <scheme>://<agent-name>:<secret-key>@<host>:<port>?mode=<agent-mode>
type DSN struct {
	// http | https | grpc | grpcs |...
	Scheme string
	// host:port
	Address string
	// agent mode (standard or embedded)
	AgentMode string

	// user (name) of the secret
	Name string
	// the secret key hashed
	SecretKeyHash string

	// If true, skip TLS certificate verification
	SkipTLSVerify bool

	key string
}

func (d *DSN) Key() string { return d.key }

// NewString generates a dsn key
func NewString(targetURL, name, secretKey, agentMode string) (string, error) {
	u, err := url.Parse(targetURL)
	if err != nil {
		return "", fmt.Errorf("failed parsing target url, reason=%v", err)
	}
	dsn := fmt.Sprintf("%s://%s:%s@%s:%s?mode=%s",
		u.Scheme, name, secretKey, u.Hostname(), u.Port(), agentMode)
	_, err = Parse(dsn)
	return dsn, err
}

// New generates a dsn key without the mode query string option
func New(targetURL, name, secretKey string) (string, error) {
	u, err := url.Parse(targetURL)
	if err != nil {
		return "", fmt.Errorf("failed parsing target url, reason=%v", err)
	}
	dsn := fmt.Sprintf("%s://%s:%s@%s:%s", u.Scheme, name, secretKey, u.Hostname(), u.Port())
	_, err = Parse(dsn)
	return dsn, err
}

// Parse a key dsn v hashing the secret key and returning the hashed value
//
// format: <scheme>://<agent-name>:<secret-key>@<host>:<port>?mode=<agent-mode>
func Parse(keyDsn string) (*DSN, error) {
	if keyDsn == "" {
		return nil, ErrEmpty
	}
	u, err := url.Parse(keyDsn)
	if err != nil {
		return nil, err
	}

	// it is an old dsn, maintain compatibility
	// <scheme>://<host>:<port>/<secretkey-hash>
	if len(u.Path) == 65 {
		// hash the whole dsn instead only the secret key
		secretKeyHash, err := hash256Key(keyDsn)
		return &DSN{
			Scheme:        u.Scheme,
			Address:       u.Host,
			AgentMode:     pb.AgentModeEmbeddedType,
			Name:          "",
			SecretKeyHash: secretKeyHash,
			SkipTLSVerify: false,
			key:           keyDsn,
		}, err
	}
	agentMode := u.Query().Get("mode")
	skipTLSVerify := u.Query().Get("skip_tls_verify") == "true"
	secretKey, _ := u.User.Password()
	if secretKey == "" {
		return nil, ErrSecretKeyNotFound
	}
	secretKeyHash, err := hash256Key(secretKey)
	return &DSN{
		Scheme:        u.Scheme,
		Address:       u.Host,
		AgentMode:     agentMode,
		Name:          u.User.Username(),
		SecretKeyHash: secretKeyHash,
		SkipTLSVerify: skipTLSVerify,
		key:           keyDsn,
	}, err
}

func hash256Key(secretKey string) (secret256Hash string, err error) {
	h := sha256.New()
	if _, err := h.Write([]byte(secretKey)); err != nil {
		return "", fmt.Errorf("failed hashing secret key, err=%v", err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
