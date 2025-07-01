package log

// EventFormatter defines how to format different event types
type EventFormatter interface {
	FormatHuman(fields map[string]interface{}, msg string) string
	FormatVerbose(fields map[string]interface{}, msg string) string
}

// EventRegistry holds all event formatters
type EventRegistry map[string]EventFormatter

// Global registry of event formatters
var Events = EventRegistry{
	"agent.start":            &AgentStartFormatter{},
	"session.start":          &SessionStartFormatter{},
	"session.cleanup":        &SessionCleanupFormatter{},
	"connection.start":       &ConnectionFormatter{},
	"connection.established": &ConnectionEstablishedFormatter{},
	"command.exec":           &CommandFormatter{},
	"command.result":         &CommandResultFormatter{},
	"agent.shutdown":         &AgentShutdownFormatter{},
}
