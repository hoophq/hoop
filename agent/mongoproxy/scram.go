package mongoproxy

import (
	"crypto/md5"
	"fmt"
	"io"

	"github.com/xdg-go/scram"
)

const (
	// scramSHA1 holds the mechanism name "SCRAM-SHA-1"
	scramSHA1 = "SCRAM-SHA-1"

	// scramSHA256 holds the mechanism name "SCRAM-SHA-256"
	scramSHA256 = "SCRAM-SHA-256"

	defaultProxyUser = "noop"
	defaultProxyPwd  = "noop"

	// changes minimum required scram PBKDF2 iteration count.
	defaultScramMinIterations int = 4096
)

func newScramClient(mechanism string, username, password string) (client *scram.Client, err error) {
	switch mechanism {
	case scramSHA1:
		// client, err = scram.SHA1.NewClient(username, password, "")
		passdigest := mongoPasswordDigest(username, password)
		client, err = scram.SHA1.NewClientUnprepped(username, passdigest, "")
		if err != nil {
			return nil, fmt.Errorf("error initializing SCRAM-SHA-1 client: %v", err)
		}
	case scramSHA256:
		client, err = scram.SHA256.NewClient(username, password, "")
		if err != nil {
			return nil, fmt.Errorf("error initializing SCRAM-SHA-256 client: %v", err)
		}
	default:
		return nil, fmt.Errorf("mechanism %v is not supported", mechanism)
	}
	return client.WithMinIterations(defaultScramMinIterations), nil
}

func newScramServerWithHardCodedCredentials(authMechanism string) (*scram.Server, error) {
	client, err := newScramClient(authMechanism, defaultProxyUser, defaultProxyPwd)
	if err != nil {
		return nil, err
	}
	stored := client.GetStoredCredentials(scram.KeyFactors{Salt: "server-nonce", Iters: defaultScramMinIterations})
	switch authMechanism {
	case scramSHA1:
		return scram.SHA1.NewServer(func(s string) (scram.StoredCredentials, error) { return stored, nil })
	case scramSHA256:
		return scram.SHA256.NewServer(func(s string) (scram.StoredCredentials, error) { return stored, nil })
	}
	return nil, fmt.Errorf("invalid auth mechanism %v", authMechanism)
}

func mongoPasswordDigest(username, password string) string {
	h := md5.New()
	_, _ = io.WriteString(h, username)
	_, _ = io.WriteString(h, ":mongo:")
	_, _ = io.WriteString(h, password)
	return fmt.Sprintf("%x", h.Sum(nil))
}
