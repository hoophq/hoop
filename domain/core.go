package domain

type (
	Org struct {
		Id   string `json:"id"   edn:"xt/id"`
		Name string `json:"name" edn:"org/name" binding:"required"`
	}

	User struct {
		Id    string `json:"id"    edn:"xt/id"`
		Org   string `json:"-"     edn:"user/org"`
		Name  string `json:"name"  edn:"user/name"`
		Email string `json:"email" edn:"user/email" binding:"required"`
	}
)

const (
	DBSecretProvider SecretProvider = "database"
)
