package templates

import (
	"encoding/base64"
	"fmt"
	"net"
	"os/exec"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	gitssh "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/hoophq/hoop/common/log"
	"golang.org/x/crypto/ssh"
)

type RunbookConfig struct {
	GitURL string
	Auth   transport.AuthMethod
}

var sshKeyScanKnownHostsContent string

// parseKnownHosts parses a known hosts file format from the plugin configuration if it's available
// or from the execution of ssh-keyscan.
// The content from ssh-keyscan is cached in memory and reused on subsequente calls
func parseKnownHosts(envVars map[string]string) (string, map[string]string, error) {
	if len(envVars) == 0 {
		return "", nil, fmt.Errorf("missing (empty) required plugin configuration: GIT_URL")
	}
	gitURLEnc := envVars["GIT_URL"]
	if gitURLEnc == "" {
		return "", nil, fmt.Errorf("missing required plugin configuration: GIT_URL")
	}
	gitURLBytes, err := base64.StdEncoding.DecodeString(gitURLEnc)
	if err != nil {
		return "", nil, fmt.Errorf("failed decoding GIT_URL")
	}
	// no need to use known hosts file if it's http protocol
	if _, ok := envVars["GIT_PASSWORD"]; ok {
		return string(gitURLBytes), map[string]string{}, nil
	}

	sshKnownHostsEnc := envVars["GIT_SSH_KNOWN_HOSTS"]
	sshKnownHosts := map[string]string{}
	var sshKnownHostsContent []byte
	// ssh-keyscan should be executed only once and then cached
	if sshKnownHostsEnc == "" && len(sshKeyScanKnownHostsContent) == 0 {
		log.Infof("obtaining keys from remote git hosts: ssh-keyscan github.com gitlab.com")
		sshKnownHostsContent, err = exec.Command("ssh-keyscan", "github.com", "gitlab.com").Output()
		if err != nil {
			return "", nil, fmt.Errorf("failed executing ssh-keyscan, err=%v", err)
		}
		sshKeyScanKnownHostsContent = string(sshKnownHostsContent)
	}

	// decode it from plugin configuration
	if sshKnownHostsEnc != "" {
		sshKnownHostsContent, err = base64.StdEncoding.DecodeString(sshKnownHostsEnc)
		if err != nil {
			return "", nil, fmt.Errorf("failed decoding GIT_SSH_KNOWN_HOSTS")
		}
	}

	if len(sshKnownHostsContent) == 0 {
		sshKnownHostsContent = []byte(sshKeyScanKnownHostsContent)
	}

	for _, knownHostsLine := range strings.Split(string(sshKnownHostsContent), "\n") {
		// <host> <alg> <key>
		parts := strings.Split(knownHostsLine, " ")
		if len(parts) != 3 {
			continue
		}
		storeKey := fmt.Sprintf("%s:%s", strings.Split(parts[0], ":")[0], parts[1])
		sshKnownHosts[storeKey] = parts[1] + " " + parts[2]
	}
	return string(gitURLBytes), sshKnownHosts, nil
}

func NewRunbookConfig(envVars map[string]string) (*RunbookConfig, error) {
	gitURL, knownHosts, err := parseKnownHosts(envVars)
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
		// This callback prevents MITM attacks
		//
		// It uses a custom callback function instead of relying in the known hosts
		// file from the filesystem.
		auth.HostKeyCallback = trustedHostKeyCallback(knownHosts)
		return &RunbookConfig{gitURL, auth}, nil
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
		return &RunbookConfig{
			GitURL: gitURL,
			Auth:   &githttp.BasicAuth{Username: string(gitUser), Password: string(gitPassword)},
		}, nil
	}
	return &RunbookConfig{GitURL: gitURL}, nil
}

// e.g. "ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTY...."
func knownHostKey(k ssh.PublicKey) string {
	return k.Type() + " " + base64.StdEncoding.EncodeToString(k.Marshal())
}

func knowHostMapKey(hostname string, pub ssh.PublicKey) string {
	return fmt.Sprintf("%s:%s", strings.Split(hostname, ":")[0], pub.Type())
}

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
