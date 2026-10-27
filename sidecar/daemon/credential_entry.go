package daemon

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// The per-source checks a pool entry passes on the control plane, where the
// sources themselves are not linked, and again in the source at login. Each
// reads the entry and the block alone: no file, no network.

// ValidateSecretFilesEntry checks a secret_files entry. A user is optional
// here: an http lane may hold a bearer alone, and the lane validator
// requires one on a database lane. dir is the block's directory, cleaned.
func ValidateSecretFilesEntry(dir string, e CredentialEntry) error {
	if e.PasswordFile == "" {
		return errors.New("password_file is required")
	}
	if e.User != "" && e.UserFile != "" {
		return errors.New("user and user_file both name the user; keep one")
	}
	for _, f := range []string{e.PasswordFile, e.UserFile} {
		if f == "" {
			continue
		}
		if _, err := ResolveSecretFile(dir, f); err != nil {
			return err
		}
	}
	return nil
}

// ResolveSecretFile places a file under the secret_files directory. A
// relative path joins it; an absolute one has to already be inside, so a
// pool entry cannot read a file the block did not put in reach.
func ResolveSecretFile(dir, file string) (string, error) {
	var full string
	if filepath.IsAbs(file) {
		full = filepath.Clean(file)
	} else {
		full = filepath.Join(dir, file)
	}
	rel, err := filepath.Rel(dir, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside credentials.secret_files.dir %s", file, dir)
	}
	return full, nil
}

// ValidateGCPImpersonationEntry checks a gcp_impersonation entry.
func ValidateGCPImpersonationEntry(e CredentialEntry) error {
	if e.ServiceAccount == "" {
		return errors.New("service_account is required")
	}
	if !strings.Contains(e.ServiceAccount, "@") {
		return fmt.Errorf("service_account %q is not an email", e.ServiceAccount)
	}
	return nil
}

// ValidateX509Entry checks an x509 entry.
func ValidateX509Entry(e CredentialEntry) error {
	if e.CertFile == "" || e.KeyFile == "" {
		return errors.New("cert_file and key_file are required")
	}
	return nil
}

// The vault block's login methods.
const (
	VaultAuthKubernetes = "kubernetes"
	VaultAuthTokenFile  = "token_file"
)

// ValidateVaultEntry checks a vault entry.
func ValidateVaultEntry(e CredentialEntry) error {
	if e.Path == "" {
		return errors.New("path is required: the Vault API path to read")
	}
	if strings.HasPrefix(e.Path, "/") || strings.HasPrefix(e.Path, "v1/") {
		return fmt.Errorf("path %q is relative to the API root; write secret/data/<name>, not /v1/...", e.Path)
	}
	if VaultIsDynamic(e.Path) && (e.User != "" || e.UserKey != "" || e.PasswordKey != "") {
		return errors.New("a database engine path names its own user and password; user, user_key and password_key are KV settings")
	}
	return nil
}

// VaultIsDynamic reports a database secrets engine path, whose answer is a
// leased user rather than a stored secret.
func VaultIsDynamic(path string) bool {
	return strings.Contains("/"+path, "/creds/")
}

// ValidateGCPSecretManagerEntry checks a gcp_secret_manager entry.
func ValidateGCPSecretManagerEntry(e CredentialEntry) error {
	if e.Secret == "" {
		return errors.New("secret is required: projects/<project>/secrets/<name>/versions/<version>")
	}
	parts := strings.Split(e.Secret, "/")
	if len(parts) != 6 || parts[0] != "projects" || parts[2] != "secrets" || parts[4] != "versions" || parts[1] == "" || parts[3] == "" || parts[5] == "" {
		return fmt.Errorf("secret %q is not projects/<project>/secrets/<name>/versions/<version>", e.Secret)
	}
	return nil
}

// Validate checks the vault block's own fields.
func (v *VaultSourceConfig) Validate() []string {
	var problems []string
	if v.Addr == "" {
		problems = append(problems, "addr is required")
	} else if u, err := url.Parse(v.Addr); err != nil || u.Scheme != "https" || u.Host == "" {
		problems = append(problems, fmt.Sprintf("addr %q is not an https URL", v.Addr))
	}
	switch v.Auth.Type {
	case VaultAuthKubernetes:
		if v.Auth.Role == "" {
			problems = append(problems, "auth.role is required with the kubernetes method")
		}
	case VaultAuthTokenFile:
		if v.Auth.TokenFile == "" {
			problems = append(problems, "auth.token_file is required with the token_file method")
		}
		if v.Auth.Role != "" || v.Auth.Mount != "" {
			problems = append(problems, "auth.role and auth.mount are kubernetes settings, not token_file ones")
		}
	case "":
		problems = append(problems, "auth.type is required: kubernetes or token_file")
	default:
		problems = append(problems, fmt.Sprintf("auth.type %q is not kubernetes or token_file", v.Auth.Type))
	}
	return problems
}
