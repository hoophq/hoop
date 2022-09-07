package domain

type (
	Secret map[string]interface{}

	Connection struct {
		Id          string         `edn:"xt/id"`
		OrgId       string         `edn:"connection/org"`
		Name        string         `edn:"connection/name" `
		Command     []string       `edn:"connection/command"`
		Type        ConnectionType `edn:"connection/type"`
		Provider    SecretProvider `edn:"connection/provider"`
		SecretId    string         `edn:"connection/secret"`
		CreatedById string         `edn:"connection/created-by"`
	}

	ConnectionOne struct {
		ConnectionList
		Secret Secret `json:"secret" edn:"connection/secret"`
	}

	ConnectionList struct {
		Id       string         `json:"id"       edn:"xt/id"`
		Name     string         `json:"name"     edn:"connection/name"    binding:"required"`
		Command  []string       `json:"command"  edn:"connection/command" binding:"required"`
		Type     ConnectionType `json:"type"     edn:"connection/type"    binding:"required"`
		Provider SecretProvider `json:"provider" edn:"connection/provider"`
	}

	ConnectionType string
	SecretProvider string
)
