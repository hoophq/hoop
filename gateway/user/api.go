package user

type (
	Handler struct {
		Service service
	}

	service interface {
		Signup(org *Org, user *User) (txId int64, err error)
		FindBySub(sub string) (*Context, error)
	}
)
