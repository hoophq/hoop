package types

import "time"

// TxEdnStruct must be a struct containing edn fields.
// See https://github.com/go-edn/edn.
type TxEdnStruct any

type TxResponse struct {
	TxID   int64     `edn:"xtdb.api/tx-id"`
	TxTime time.Time `edn:"xtdb.api/tx-time"`
}

type APIContext struct {
	OrgID  string
	UserID string

	UserName   string
	UserEmail  string
	UserGroups []string
}

type Client struct {
	ID                    string            `edn:"xt/id"`
	OrgID                 string            `edn:"client/org"`
	Status                ClientStatusType  `edn:"client/status"`
	RequestConnectionName string            `edn:"client/request-connection"`
	RequestPort           string            `edn:"client/request-port"`
	RequestAccessDuration time.Duration     `edn:"client/access-duration"`
	ClientMetadata        map[string]string `edn:"client/metadata"`
	ConnectedAt           time.Time         `edn:"client/connected-at"`
}
