package sidecartui

// agentEnv are the variables coding agents set in the shells they run
// commands in. Some agents give those shells a pseudo-terminal, so a
// terminal check alone would open a screen that waits for keys nobody
// presses. AI_AGENT is the generic marker any agent can set.
var agentEnv = []string{"AI_AGENT", "CLAUDECODE", "CURSOR_AGENT", "GEMINI_CLI", "CODEX_SANDBOX"}

// containerFiles are made by the container runtime: Docker, then Podman.
var containerFiles = []string{"/.dockerenv", "/run/.containerenv"}

// Unattended names why nobody can be at this terminal although it is one, or
// returns "" when a person may be. A coding agent drives the shell, or the
// process runs where it was deployed: a Kubernetes pod (the API server
// address is in every pod), or a container. `docker run -it` and a pod
// with tty and stdin give a terminal too; the sidecar there must log as it
// does without one, and a held statement must not wait for a key.
func Unattended(getenv func(string) string, exists func(string) bool) string {
	for _, k := range agentEnv {
		if getenv(k) != "" {
			return "a coding agent runs this shell (" + k + " is set)"
		}
	}
	if getenv("KUBERNETES_SERVICE_HOST") != "" {
		return "this runs in a Kubernetes pod"
	}
	if getenv("container") != "" {
		return "this runs in a container (container is set)"
	}
	for _, f := range containerFiles {
		if exists(f) {
			return "this runs in a container (" + f + " exists)"
		}
	}
	return ""
}
