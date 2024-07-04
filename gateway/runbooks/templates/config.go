package templates

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/go-git/go-git/v5/plumbing/transport"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/hoophq/hoop/common/log"
)

type RunbookConfig struct {
	URL  string
	Auth transport.AuthMethod
}

func NewRunbookConfig(envVars map[string]string) (*RunbookConfig, error) {
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
			log.Infof("failed parsing SSH key file, err=%v", err)
			return nil, fmt.Errorf("failed parsing SSH key file")
		}
		return &RunbookConfig{string(gitURL), auth}, nil
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
			URL:  string(gitURL),
			Auth: &githttp.BasicAuth{Username: string(gitUser), Password: string(gitPassword)},
		}, nil
	}
	return &RunbookConfig{URL: string(gitURL)}, nil
}

// SSHKeyScan runs ssh-keyscan command to known git providers like gitlab and github.
// This function should be replaced by go code in the future to accomodate other git servers
//
// References:
//
// https://github.com/go-git/go-git/issues/638
//
// https://cyruslab.net/2020/10/23/golang-how-to-write-ssh-hostkeycallback/
//
// https://github.com/melbahja/goph/blob/3176130089e72b898096df73add39a658534f9e5/hosts.go#L41
func SSHKeyScan() (string, error) {
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*20)
	defer cancelFn()
	knownHostsList := []*bytes.Buffer{}
	for _, host := range []string{"github.com", "gitlab.com"} {
		out, err := exec.CommandContext(ctx, "ssh-keyscan", "-H", host).Output()
		if err != nil {
			return "", fmt.Errorf("failed executing ssh-keyscan, err=%v", err)
		}
		knownHostsList = append(knownHostsList, bytes.NewBuffer(out))
	}
	homeFolder, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed retrieving home folder, err=%v", err)
	}
	sshHomeFolder := filepath.Join(homeFolder, ".ssh")
	if err := os.MkdirAll(sshHomeFolder, 07000); err != nil {
		return "", fmt.Errorf("failed creating folder %v, err=%v", sshHomeFolder, err)
	}
	knownHostsFilePath := filepath.Join(sshHomeFolder, "known_hosts")
	f, err := os.OpenFile(knownHostsFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return knownHostsFilePath, fmt.Errorf("failed opening file %v, err=%v", knownHostsFilePath, err)
	}
	defer f.Close()
	for _, hostString := range knownHostsList {
		if _, err := f.Write(hostString.Bytes()); err != nil {
			return knownHostsFilePath, fmt.Errorf("failed writing host to %v, err=%v", knownHostsFilePath, err)
		}
	}
	return knownHostsFilePath, nil
}
