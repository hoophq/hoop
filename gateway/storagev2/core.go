package storagev2

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/common/log"
	"github.com/runopsio/hoop/gateway/analytics"
	"github.com/runopsio/hoop/gateway/storagev2/types"
)

type Store struct {
	client  HTTPClient
	address string
}

// HTTPClient is an interface for testing a request object.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

func NewStorage(httpClient HTTPClient) *Store {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	s := &Store{client: httpClient, address: os.Getenv("XTDB_ADDRESS")}
	if s.address == "" {
		s.address = "http://localhost:3000"
	}
	return s
}

func (s *Store) URL() *url.URL {
	u, _ := url.Parse(s.address)
	return u
}

func (s *Store) SetURL(xtdbURL string) { s.address = xtdbURL }

func (s *Store) Put(trxs ...types.TxObject) (*types.TxResponse, error) {
	return submitPutTx(s.client, s.address, trxs...)
}

func (s *Store) Evict(xtIDs ...string) (*types.TxResponse, error) {
	return submitEvictTx(s.client, s.address, xtIDs...)
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

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return data, nil
	case http.StatusNotFound:
		return nil, nil
	default:
		return data, fmt.Errorf("failed fetching entity, status=%v, data=%v",
			resp.StatusCode, string(data))
	}
}

const ContextKey = "storagev2"

type Context struct {
	*Store
	*types.APIContext
	dsnctx  *types.DSNContext
	segment *analytics.Segment
}

func ParseContext(c *gin.Context) *Context {
	obj, ok := c.Get(ContextKey)
	if !ok {
		log.Warnf("failed obtaing context from *gin.Context for key %q", ContextKey)
		return &Context{
			Store:      NewStorage(nil),
			APIContext: &types.APIContext{},
			segment:    nil}
	}
	ctx, _ := obj.(*Context)
	if ctx == nil {
		log.Warnf("failed type casting value to *Context")
		return &Context{
			Store:      NewStorage(nil),
			APIContext: &types.APIContext{},
			segment:    nil}
	}
	return ctx
}

func NewContext(userID, orgID string, store *Store) *Context {
	return &Context{
		Store:      store,
		APIContext: &types.APIContext{UserID: userID, OrgID: orgID},
		segment:    nil}
}

// NewOrganizationContext returns a context without a user
func NewOrganizationContext(orgID string, store *Store) *Context {
	return NewContext("", orgID, store)
}

func NewDSNContext(entityID, orgID, clientKeyName string, store *Store) *Context {
	return &Context{
		Store:      store,
		dsnctx:     &types.DSNContext{EntityID: entityID, OrgID: orgID, ClientKeyName: clientKeyName},
		APIContext: &types.APIContext{OrgID: orgID},
		segment:    nil,
	}
}

func (c *Context) WithUserInfo(name, email, status string, groups []string) *Context {
	c.UserName = name
	c.UserEmail = email
	c.UserGroups = groups
	c.UserStatus = status
	return c
}

func (c *Context) WithOrgName(orgName string) *Context {
	c.OrgName = orgName
	return c
}

func (c *Context) WithApiURL(apiURL string) *Context {
	c.ApiURL = apiURL
	return c
}

func (c *Context) WithGrpcURL(grpcURL string) *Context {
	c.GrpcURL = grpcURL
	return c
}

func (c *Context) Analytics() *analytics.Segment {
	if c.segment == nil {
		c.segment = analytics.New()
		return c.segment
	}
	return c.segment
}

func (c *Context) DSN() *types.DSNContext { return c.dsnctx }

func (c *Context) GetOrgID() string { return c.OrgID }
