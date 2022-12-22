package notification

type (
	Notification struct {
		Title        string
		Message      string
		Destinations []string
	}

	Service interface {
		Send(Notification)
	}
)
