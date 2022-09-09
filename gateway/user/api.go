package user

type (
	Handler struct {
		Service service
	}

	service interface {
		Signup(org *Org, user *User) (txId int64, err error)
		ContextByEmail(email string) (*Context, error)
	}
)
