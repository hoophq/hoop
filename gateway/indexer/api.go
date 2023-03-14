package indexer

import (
	"fmt"
	"log"
	"net/http"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/highlight/highlighter/ansi"
	"github.com/gin-gonic/gin"
	"github.com/runopsio/hoop/gateway/indexer/searchquery"
	"github.com/runopsio/hoop/gateway/user"
)

const maxSearchLimit = 50

type Handler struct{}

type SearchRequest struct {
	QueryString string              `json:"query" binding:"required"`
	Limit       int                 `json:"limit"`
	Offset      int                 `json:"offset"`
	Highlight   bool                `json:"highlight"`
	Fields      []string            `json:"fields"`
	Facets      bleve.FacetsRequest `json:"facets"`
}

func (a *Handler) Search(c *gin.Context) {
	obj, _ := c.Get("context")
	ctx := obj.(*user.Context)
	if ctx.User.Id == "" || ctx.Org.Id == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"message": "missing org or user identifier"})
		return
	}
	var req SearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	bleveSearchRequest, err := req.parse(ctx.User.Id, ctx.User.IsAdmin())
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"message": err.Error()})
		return
	}
	index, err := Open(ctx.User.Org)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}

	log.Printf("org=%v, user=%v, query=[%v] - searching", ctx.Org.Id, ctx.User.Id, req.QueryString)
	searchResult, err := index.Search(bleveSearchRequest)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, searchResult)
}

func (s *SearchRequest) parse(userID string, isAdmin bool) (*bleve.SearchRequest, error) {
	if userID == "" {
		return nil, fmt.Errorf("missing user identifier")
	}
	var scopeUserID string
	if !isAdmin {
		scopeUserID = userID
	}

	finalQuery, err := searchquery.Parse(scopeUserID, s.QueryString)
	if err != nil {
		return nil, err
	}

	if s.Limit > maxSearchLimit {
		s.Limit = maxSearchLimit
	}

	req := bleve.NewSearchRequestOptions(finalQuery, s.Limit, s.Offset, false)
	req.Fields = defaultFields
	if len(s.Fields) > 0 {
		req.Fields = s.Fields
	}
	if s.Highlight {
		req.Highlight = bleve.NewHighlightWithStyle(ansi.Name)
	}
	if len(s.Facets) > 0 {
		req.Facets = s.Facets
	}
	return req, s.validate(req)
}

func (s *SearchRequest) AddFacet(facetName string, f *bleve.FacetRequest) {
	if s.Facets == nil {
		s.Facets = make(bleve.FacetsRequest, 1)
	}
	s.Facets[facetName] = f
}

func (s *SearchRequest) validate(req *bleve.SearchRequest) error {
	for _, requestField := range req.Fields {
		hasField := false
		for _, existentField := range registeredFields {
			if requestField == existentField {
				hasField = true
				break
			}
		}
		if !hasField {
			return fmt.Errorf("field '%v' not available", requestField)
		}
	}
	return req.Validate()
}

func NewSearchRequest(query string, limit, offset int, highlight bool) *SearchRequest {
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	return &SearchRequest{
		QueryString: query,
		Limit:       limit,
		Offset:      offset,
		Highlight:   highlight,
	}
}
