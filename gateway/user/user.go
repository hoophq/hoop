package user

type (
	Service struct {
		Storage storage
	}

	storage interface {
		Signup(org *Org, user *User) (txId int64, err error)
		ContextByEmail(email string) (*Context, error)
	}

	Context struct {
		Org  *Org
		User *User
	}

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

func (s *Service) Signup(org *Org, user *User) (txId int64, err error) {
	return s.Storage.Signup(org, user)
}

func (s *Service) ContextByEmail(email string) (*Context, error) {
	return s.Storage.ContextByEmail(email)
}
