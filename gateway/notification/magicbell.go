package notification

import (
	"fmt"
	"net/http"
	"os"
)

type (
	MagicBell struct {
		client    http.Client
		apiKey    string
		apiSecret string
	}
)

func NewMagicBell() *MagicBell {
	return &MagicBell{
		client:    http.Client{},
		apiKey:    os.Getenv("MAGIC_BELL_API_KEY"),
		apiSecret: os.Getenv("MAGIC_BELL_API_SECRET"),
	}
}

func (m *MagicBell) Send(notif Notification) {
	if m.apiKey != "" && m.apiSecret != "" {
		fmt.Printf("Sending magicbell event with title [%s]\n", notif.Title)
	}
}
