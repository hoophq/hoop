package analytics

import (
	"github.com/segmentio/analytics-go/v3"
	"os"
)

type (
	Segment struct {
		analytics.Client
	}

	Analytics interface {
		Identify(name, email string, traits map[string]any)
		Track(email, eventName string, properties map[string]any)
	}
)

func New() *Segment {
	key := os.Getenv("SEGMENT_KEY")
	if key == "" {
		return &Segment{}
	}

	client := analytics.New(key)
	return &Segment{client}
}

func (s *Segment) Identify(name, email string, traits map[string]any) {
	if s.Client == nil {
		return
	}
}

func (s *Segment) Track(email, eventName string, properties map[string]any) {
	if s.Client == nil {
		return
	}
}
