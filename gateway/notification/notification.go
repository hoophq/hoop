package notification

type (
	Notification struct {
		Title      string
		Message    string
		Recipients []string
	}

	Service interface {
		Send(Notification)
		IsFullyConfigured() bool
	}
)
