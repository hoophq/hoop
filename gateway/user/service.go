package user

type (
	Service struct {
		Storage storage
	}

	storage interface {
		Signup(org *Org, user *User) (txId int64, err error)
		FindById(email string) (*Context, error)
		Persist(user interface{}) (int64, error)
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
		Id     string     `json:"id"     edn:"xt/id"`
		Org    string     `json:"-"      edn:"user/org"`
		Name   string     `json:"name"   edn:"user/name"`
		Email  string     `json:"email"  edn:"user/email" binding:"required"`
		Status StatusType `json:"status" edn:"user/status"`
	}

	StatusType string
)

const (
	StatusActive StatusType = "active"
)

func (s *Service) Signup(org *Org, user *User) (txId int64, err error) {
	return s.Storage.Signup(org, user)
}

func (s *Service) FindBySub(sub string) (*Context, error) {
	return s.Storage.FindById(sub)
}

func (s *Service) Persist(user interface{}) error {
	_, err := s.Storage.Persist(user)
	if err != nil {
		return err
	}
	return nil
}
