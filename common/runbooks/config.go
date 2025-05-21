package runbooks

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/hoophq/hoop/common/log"
	"golang.org/x/crypto/ssh"
)

var ErrEmptyConfiguration = errors.New("missing (empty) required plugin configuration: GIT_URL")

type Config struct {
	GitURL           string
	HookCacheTTL     *time.Duration
	Auth             transport.AuthMethod
	sshKnownHostsEnc string
}

// NewConfig creates a new RunbookConfig from the given runbook plugin configuration
func NewConfig(envVars map[string]string) (*Config, error) {
	if len(envVars) == 0 {
		return nil, ErrEmptyConfiguration
	}
	gitURL, err := base64.StdEncoding.DecodeString(envVars["GIT_URL"])
	if err != nil {
		return nil, ErrEmptyConfiguration
	}

	hookCacheTTL, err := parseRunbookHookCacheTTLConfig(envVars)
	if err != nil {
		return nil, err
	}
	gitUserEnc := envVars["GIT_USER"]
	gitPasswordEnc := envVars["GIT_PASSWORD"]
	sshKeyEnc := envVars["GIT_SSH_KEY"]
	sshUserEnc := envVars["GIT_SSH_USER"]
	sshKeyPassEnc := envVars["GIT_SSH_KEYPASS"]
	switch {
	case sshKeyEnc != "":
		pemBytes, err := base64.StdEncoding.DecodeString(sshKeyEnc)
		if err != nil {
			return nil, fmt.Errorf("failed decoding GIT_SSH_KEY")
		}
		sshUser := []byte(`git`)
		if sshUserEnc != "" {
			sshUser, err = base64.StdEncoding.DecodeString(sshUserEnc)
			if err != nil {
				return nil, fmt.Errorf("failed decoding GIT_SSH_USER")
			}
		}
		sshKeyPass := []byte(``)
		if sshKeyPassEnc != "" {
			sshKeyPass, err = base64.StdEncoding.DecodeString(sshKeyPassEnc)
			if err != nil {
				return nil, fmt.Errorf("failed decoding GIT_SSH_KEYPASS")
			}
		}
		auth, err := gitssh.NewPublicKeys(string(sshUser), pemBytes, string(sshKeyPass))
		if err != nil {
			log.Infof("failed parsing SSH key file, err=%v", err)
			return nil, fmt.Errorf("failed parsing SSH key file")
		}
		return &Config{
			GitURL:           string(gitURL),
			HookCacheTTL:     hookCacheTTL,
			Auth:             auth,
			sshKnownHostsEnc: envVars["GIT_SSH_KNOWN_HOSTS"],
		}, nil
	case gitPasswordEnc != "":
		gitPassword, err := base64.StdEncoding.DecodeString(gitPasswordEnc)
		if err != nil {
			return nil, fmt.Errorf("failed decoding GIT_PASSWORD")
		}
		gitUser := []byte(`oauth2`)
		if gitUserEnc != "" {
			gitUser, err = base64.StdEncoding.DecodeString(gitUserEnc)
			if err != nil {
				return nil, fmt.Errorf("failed decoding GIT_USER")
			}
		}
		return &Config{
			GitURL:       string(gitURL),
			Auth:         &githttp.BasicAuth{Username: string(gitUser), Password: string(gitPassword)},
			HookCacheTTL: hookCacheTTL,
		}, nil
	}
	return &Config{GitURL: string(gitURL), HookCacheTTL: hookCacheTTL}, nil
}

var globalSSHKeyScanKnownHostsContent string

// loadKnownHosts parses a known hosts file format from the plugin configuration if it's available.
// It fallback loading the known hosts by issuing a ssh-keyscan once and cache the content in memory.
//
// If it is not a public key authentication configuration, this method does nothing
func (c *Config) loadKnownHosts() (err error) {
	auth, ok := c.Auth.(*gitssh.PublicKeys)
	if !ok {
		return nil
	}

	// load from cache if it's available
	if globalSSHKeyScanKnownHostsContent != "" {
		knownHosts := parseKnownHostsFileContent(globalSSHKeyScanKnownHostsContent)
		auth.HostKeyCallback = trustedHostKeyCallback(knownHosts)
		return nil
	}

	// ssh-keyscan should be executed only once and then cached
	if c.sshKnownHostsEnc == "" && len(globalSSHKeyScanKnownHostsContent) == 0 {
		knownHostsContent, err := sshKeyScan()
		if err != nil {
			return fmt.Errorf("failed executing ssh-keyscan, err=%v", err)
		}
		// ssh-keyscan should be executed only once and then cached
		globalSSHKeyScanKnownHostsContent = knownHostsContent
		knownHosts := parseKnownHostsFileContent(knownHostsContent)
		auth.HostKeyCallback = trustedHostKeyCallback(knownHosts)
		return nil
	}

	// fallback loading from the plugin configuration
	knownHostsContent, err := base64.StdEncoding.DecodeString(c.sshKnownHostsEnc)
	if err != nil {
		return fmt.Errorf("failed decoding GIT_SSH_KNOWN_HOSTS")
	}
	knownHosts := parseKnownHostsFileContent(string(knownHostsContent))
	auth.HostKeyCallback = trustedHostKeyCallback(knownHosts)
	return nil
}

func parseRunbookHookCacheTTLConfig(envVars map[string]string) (*time.Duration, error) {
	if len(envVars) == 0 {
		return nil, nil
	}
	configTTLEnc, ok := envVars["GIT_HOOK_CONFIG_TTL"]
	if !ok {
		return nil, nil
	}
	configTTLStr, err := base64.StdEncoding.DecodeString(configTTLEnc)
	if err != nil {
		return nil, fmt.Errorf("failed decoding GIT_HOOK_CONFIG_TTL, %v", err)
	}
	configTTL, err := strconv.Atoi(string(configTTLStr))
	if err != nil {
		return nil, fmt.Errorf("failed parsing GIT_HOOK_CONFIG_TTL, %v", err)
	}
	d := time.Duration(configTTL) * time.Second
	return &d, nil
}

func sshKeyScan() (string, error) {
	log.Infof("obtaining keys from remote git hosts: ssh-keyscan github.com gitlab.com")
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
	defer cancelFn()
	sshKnownHostsContent, err := exec.CommandContext(ctx, "ssh-keyscan", "github.com", "gitlab.com").Output()
	if err != nil {
		return "", fmt.Errorf("failed executing ssh-keyscan, err=%v", err)
	}
	return string(sshKnownHostsContent), nil
}

func parseKnownHostsFileContent(knownHostsContent string) map[string]string {
	sshKnownHosts := map[string]string{}
	for _, knownHostsLine := range strings.Split(knownHostsContent, "\n") {
		// <host> <alg> <key>
		parts := strings.Split(knownHostsLine, " ")
		if len(parts) != 3 {
			continue
		}
		storeKey := fmt.Sprintf("%s:%s", strings.Split(parts[0], ":")[0], parts[1])
		sshKnownHosts[storeKey] = parts[1] + " " + parts[2]
	}
	return sshKnownHosts
}

// e.g. "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTY...."
func knownHostKey(k ssh.PublicKey) string {
	return k.Type() + " " + base64.StdEncoding.EncodeToString(k.Marshal())
}

func knowHostMapKey(hostname string, pub ssh.PublicKey) string {
	return fmt.Sprintf("%s:%s", strings.Split(hostname, ":")[0], pub.Type())
}

// This callback prevents MITM attacks
//
// It uses a custom callback function instead of relying in the known hosts
// file from the filesystem.
func trustedHostKeyCallback(knownHostsStore map[string]string) ssh.HostKeyCallback {
	return func(hostname string, _ net.Addr, k ssh.PublicKey) error {
		storeKey := knowHostMapKey(hostname, k)
		khKey, trustedKey := knownHostKey(k), knownHostsStore[storeKey]
		if trustedKey != khKey {
			return fmt.Errorf("ssh-key failed verification for %s. Want %q, Got %q", storeKey, trustedKey, khKey)
		}
		return nil
	}
}
