package ssmproxy

type ssmStartSessionPacket struct {
	Target string `json:"Target"`
}

type ssmStartSessionResponsePacket struct {
	SessionId  string `json:"SessionId"`
	StreamUrl  string `json:"StreamUrl"`
	TokenValue string `json:"TokenValue"`
}
