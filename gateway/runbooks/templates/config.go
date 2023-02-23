package templates

import (
	"encoding/base64"
	"fmt"
	"log"

	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
)

type RunbookConfig struct {
	URL        string
	Auth       transport.AuthMethod
	PathPrefix string
}

func NewRunbookConfig(pathPrefix string, envVars map[string]string) (*RunbookConfig, error) {
	gitURLEnc := envVars["GIT_URL"]
	if gitURLEnc == "" {
		return nil, fmt.Errorf("missing required plugin configuration: GIT_URL")
	}
	gitURL, err := base64.StdEncoding.DecodeString(gitURLEnc)
	if err != nil {
		return nil, fmt.Errorf("failed decoding GIT_URL")
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
		auth, err := ssh.NewPublicKeys(string(sshUser), pemBytes, string(sshKeyPass))
		if err != nil {
			log.Printf("failed parsing SSH key file, err=%v", err)
			return nil, fmt.Errorf("failed parsing SSH key file")
		}
		return &RunbookConfig{string(gitURL), auth, ""}, nil
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
			URL:        string(gitURL),
			Auth:       &githttp.BasicAuth{Username: string(gitUser), Password: string(gitPassword)},
			PathPrefix: pathPrefix,
		}, nil
	}
	return &RunbookConfig{URL: string(gitURL)}, nil
}
