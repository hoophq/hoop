package dsnkeys

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"

	pb "github.com/runopsio/hoop/common/proto"
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

	// temporary to support migration
	// indicates if the gateway should connect the agent using v2 api
	ApiV2 bool

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
			key:           keyDsn,
		}, err
	}
	agentMode := u.Query().Get("mode")
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
		ApiV2:         u.Query().Get("v2") == "true",
		key:           keyDsn,
	}, err
}

func GenerateSecureRandomKey() (secretKey, secretKeyHash string, err error) {
	secretRandomBytes := make([]byte, 32)
	_, err = rand.Read(secretRandomBytes)
	if err != nil {
		return "", "", fmt.Errorf("failed generating entropy, err=%v", err)
	}
	secretKey = base64.RawURLEncoding.EncodeToString(secretRandomBytes)
	secretKey = "xagt-" + secretKey
	secretKeyHash, err = hash256Key(secretKey)
	if err != nil {
		return "", "", fmt.Errorf("failed generating secret hash, err=%v", err)
	}
	return secretKey, secretKeyHash, err
}

func hash256Key(secretKey string) (secret256Hash string, err error) {
	h := sha256.New()
	if _, err := h.Write([]byte(secretKey)); err != nil {
		return "", fmt.Errorf("failed hashing secret key, err=%v", err)
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
