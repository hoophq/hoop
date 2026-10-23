package analytics

import (
	"os"
	"strings"
)

// InstallSourceEnvVar names the hoop template that started the sidecar, so
// adoption of one install path can be counted: the sidecar id is hashed and
// cannot say it. Set by the template, not by an operator.
const InstallSourceEnvVar = "HOOP_SIDECAR_INSTALL_SOURCE"

// Install source values. A fixed vocabulary rather than a sanitized string,
// because any free text an operator can set can also carry a customer name.
const (
	InstallSourceManagedAgentsHTTP       = "managed-agents-http"       // deploy/managed-agents/http
	InstallSourceManagedAgentsKubernetes = "managed-agents-kubernetes" // deploy/managed-agents/kubernetes
	InstallSourceOpenAIAgentsHTTP        = "openai-agents-http"        // deploy/openai-agents/http
	InstallSourceOpenAIAgentsKubernetes  = "openai-agents-kubernetes"  // deploy/openai-agents/kubernetes
	InstallSourceOther                   = "other"                     // set, but not a value this build knows
)

// InstallSource reports the install path from InstallSourceEnvVar, or ""
// when it is unset; the property is then omitted. A value this build does
// not know reports InstallSourceOther, never the value itself: an older
// sidecar started by a newer template still counts as set.
func InstallSource() string {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(InstallSourceEnvVar)))
	switch v {
	case "":
		return ""
	case InstallSourceManagedAgentsHTTP, InstallSourceManagedAgentsKubernetes,
		InstallSourceOpenAIAgentsHTTP, InstallSourceOpenAIAgentsKubernetes:
		return v
	}
	return InstallSourceOther
}
