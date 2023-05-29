package storagev2

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type Store struct {
	client  http.Client
	address string
}

func NewStorage() *Store {
	s := &Store{client: http.Client{}, address: os.Getenv("XTDB_ADDRESS")}
	if s.address == "" {
		s.address = "http://localhost:3000"
	}
	return s
}

func (s *Store) Put(trxs ...types.TxEdnStruct) (*types.TxResponse, error) {
	return submitPutTx(s.client, s.address, trxs...)
}

func (s *Store) Query(ednQuery string) ([]byte, error) {
	url := fmt.Sprintf("%s/_xtdb/query", s.address)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer([]byte(ednQuery)))
	if err != nil {
		return nil, err
	}

	req.Header.Set("accept", "application/edn")
	req.Header.Set("content-type", "application/edn")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return b, nil
}

func (s *Store) GetEntity(xtID string) ([]byte, error) {
	url := fmt.Sprintf("%s/_xtdb/entity", s.address)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("accept", "application/edn")

	q := req.URL.Query()
	q.Add("eid", xtID)
	req.URL.RawQuery = q.Encode()

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, err
		}
		return b, nil
	}

	return nil, nil
}

const ContextKey = "storagev2"

type Context struct {
	*Store
	*types.APIContext
}

func ParseContext(c *gin.Context) *Context {
	obj, ok := c.Get(ContextKey)
	if !ok {
		log.Warnf("failed obtaing context from *gin.Context for key %q", ContextKey)
		return &Context{NewStorage(), &types.APIContext{}}
	}
	ctx, _ := obj.(*Context)
	if ctx == nil {
		log.Warnf("failed type casting value to *Context")
		return &Context{NewStorage(), &types.APIContext{}}
	}
	return ctx
}

func NewContext(userID, orgID string, store *Store) *Context {
	return &Context{store, &types.APIContext{UserID: userID, OrgID: orgID}}
}

func (c *Context) WithUserInfo(userName, userEmail string, userGroups []string) *Context {
	c.UserName = userName
	c.UserEmail = userEmail
	c.UserGroups = userGroups
	return c
}
