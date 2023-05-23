package types

import "time"

// TxEdnStruct must be a struct containing edn fields.
// See https://github.com/go-edn/edn.
type TxEdnStruct any

type TxResponse struct {
	TxID   int64     `edn:"xtdb.api/tx-id"`
	TxTime time.Time `edn:"xtdb.api/tx-time"`
}

type UserContext struct {
	UserID string
	OrgID  string
}

type AutoConnect struct {
	ID         string            `edn:"xt/id"`
	OrgId      string            `edn:"autoconnect/org"`
	User       string            `edn:"autoconnect/user"`
	Status     string            `edn:"autoconnect/status"`
	Client     map[string]string `edn:"auto-connect/client"`
	Connection map[string]string `edn:"auto-connect/connection"`
}

type Connection struct {
	ID       string   `edn:"xt/id"`
	Name     string   `edn:"connection/name"`
	IconName string   `edn:"connection/icon-name"`
	Command  []string `edn:"connection/command"`
	AgentId  string   `edn:"connection/agent"`
}
