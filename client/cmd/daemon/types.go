package daemon

type Options struct {
	ServiceName string
	ExecArgs    string
	Env         map[string]string
	UnitPath    string
	WantedBy    string
}

// Linux systemd unit data
type unitData struct {
	Description string
	ExecPath    string
	ExecArgs    string
	Env         map[string]string
	WantedBy    string
}


// OSX launchd plist data
type launchAgentData struct {
	Label                 string
	Program               string
	ProgramArgumentsExtra []string
	EnvironmentVariables  map[string]string
	RunAtLoad             bool
	KeepAlive             bool
	StandardOutPath       string
	StandardErrorPath     string
}
