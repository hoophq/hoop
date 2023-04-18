package notification

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"

	"github.com/runopsio/hoop/common/log"
)

func getBridgeUrl(configMap map[string]string) string {
	if len(configMap["bridgeUrl"]) > 0 {
		log.Printf("Using external bridge URL env var")
		return configMap["bridgeUrl"]
	}
	log.Printf("using default notifications bridge url")
	return "http://localhost:4242"
}

type (
	Matterbridge struct {
		bridgeUrl     string
		slackBotToken string
	}
)

func NewMatterbridge() *Matterbridge {
	mBridgeConfigRaw := []byte(os.Getenv("NOTIFICATIONS_BRIDGE_CONFIG"))
	var mBridgeConfigMap map[string]string
	if err := json.Unmarshal(mBridgeConfigRaw, &mBridgeConfigMap); err != nil {
		log.Fatalf("failed decoding notifications bridge config")
	}

	return &Matterbridge{
		bridgeUrl:     getBridgeUrl(mBridgeConfigMap),
		slackBotToken: mBridgeConfigMap["slackBotToken"],
	}
}

func (m *Matterbridge) Send(notification Notification) {
	values := map[string]string{
		"text":    notification.Title + "\n\n" + notification.Message,
		"gateway": "hoop-notifications-bridge", // this is hard coded due to there's no need to people choose this name since it's just a internal matterbridge thing to fing the gateway and we will set one per org
	}
	jsonData, _ := json.Marshal(values)

	log.Println("Sending notification to bridge")
	http.Post(
		m.bridgeUrl+"/api/message",
		"application/json",
		bytes.NewBuffer(jsonData),
	)
}

func (m *Matterbridge) IsFullyConfigured() bool {
	log.Printf("Testing bridge connection")
	_, err := http.Get(m.bridgeUrl)
	if err != nil {
		log.Errorf("%v", err)
		return false
	}
	return m.slackBotToken != ""
}
