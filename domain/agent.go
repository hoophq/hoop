package domain

type (
	Agent struct {
		Token       string `json:"token"    edn:"xt/id"`
		OrgId       string `json:"-"        edn:"agent/org"`
		Name        string `json:"name"     edn:"agent/name"`
		Hostname    string `json:"hostname" edn:"agent/hostname"`
		CreatedById string `json:"-"        edn:"agent/created-by"`
	}
)
