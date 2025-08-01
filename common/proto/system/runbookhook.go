package pbsystem

const (
	RunbookHookRequestType  string = "SysRunbookHookRequestType"
	RunbookHookResponseType string = "SysRunbookHookResponseType"
)

type EventSessionOpen struct {
	Verb                string         `json:"verb"`
	ConnectionName      string         `json:"connection_name"`
	ConnectionType      string         `json:"connection_type"`
	ConnectionSubType   string         `json:"connection_subtype"`
	ConnectionEnvs      map[string]any `json:"connection_envs"`
	ConnectionReviewers []string       `json:"connection_reviewers"`
	Input               string         `json:"input"`
	UserEmail           string         `json:"user_email"`
}

type EventSessionClose struct {
	ExitCode int     `json:"exit_code"`
	Output   *string `json:"output"`
}

type RunbookHookRequest struct {
	ID                string             `json:"id"`
	SID               string             `json:"sid"`
	Command           []string           `json:"command"`
	InputFile         string             `json:"input_file"`
	EventSessionOpen  *EventSessionOpen  `json:"event_session_open"`
	EventSessionClose *EventSessionClose `json:"event_session_close"`
}

type RunbookHookResponse struct {
	ID               string `json:"id"`
	ExitCode         int    `json:"exit_code"`
	Output           string `json:"output"`
	ExecutionTimeSec int    `json:"execution_time_sec"`
}
