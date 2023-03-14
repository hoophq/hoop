package indexer

import (
	"context"
	"os"
	"path"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/index/scorch"
	"github.com/blevesearch/bleve/v2/mapping"
	"github.com/runopsio/hoop/gateway/indexer/searchquery"

	// languages
	_ "github.com/blevesearch/bleve/v2/analysis/lang/en"
)

const defaultPluginPath = "/opt/hoop/indexes"

var PluginIndexPath string

func init() {
	PluginIndexPath = os.Getenv("PLUGIN_INDEX_PATH")
	if PluginIndexPath == "" {
		PluginIndexPath = defaultPluginPath
	}
}

type Session struct {
	OrgID string `json:"-"`

	ID                string `json:"session"`
	User              string `json:"user"`
	Connection        string `json:"connection"`
	ConnectionType    string `json:"connection_type"`
	Verb              string `json:"verb"`
	EventSize         int64  `json:"size"`
	Input             string `json:"input"`
	Output            string `json:"output"`
	IsInputTruncated  bool   `json:"isinput_trunc"`
	IsOutputTruncated bool   `json:"isoutput_trunc"`
	IsError           bool   `json:"error"`
	StartDate         string `json:"started"`
	EndDate           string `json:"completed"`
	Duration          int64  `json:"duration"`
}

func newDefautFieldMapping(fieldType, fieldAnalyzer string) *mapping.FieldMapping {
	return &mapping.FieldMapping{
		Type:     fieldType,
		Analyzer: fieldAnalyzer,
		Store:    true,
		Index:    true,
	}
}

func newSessionMapping() mapping.IndexMapping {
	m := bleve.NewIndexMapping()
	m.DefaultMapping.AddFieldMappingsAt(searchquery.QualifierFilterSession, newDefautFieldMapping("text", "keyword"))
	m.DefaultMapping.AddFieldMappingsAt(searchquery.QualifierFilterUser, newDefautFieldMapping("text", "keyword"))
	m.DefaultMapping.AddFieldMappingsAt(searchquery.QualifierFilterConnection, newDefautFieldMapping("text", "keyword"))
	m.DefaultMapping.AddFieldMappingsAt(searchquery.QualifierFilterConnectionType, newDefautFieldMapping("text", "keyword"))
	m.DefaultMapping.AddFieldMappingsAt(searchquery.QualifierFilterVerb, newDefautFieldMapping("text", "keyword"))
	m.DefaultMapping.AddFieldMappingsAt(searchquery.QualifierFilterSize, newDefautFieldMapping("number", ""))

	stdinFieldMapping := newDefautFieldMapping("text", "en")
	stdoutFieldMapping := newDefautFieldMapping("text", "en")
	// highlighting
	stdinFieldMapping.IncludeTermVectors = true
	stdoutFieldMapping.IncludeTermVectors = true

	m.DefaultMapping.AddFieldMappingsAt(searchquery.QualifierQueryInInput, stdinFieldMapping)
	m.DefaultMapping.AddFieldMappingsAt(searchquery.QualifierQueryInOutput, stdoutFieldMapping)

	m.DefaultMapping.AddFieldMappingsAt(searchquery.QualifierBoolInputTruncated, newDefautFieldMapping("boolean", ""))
	m.DefaultMapping.AddFieldMappingsAt(searchquery.QualifierBoolOutputTruncated, newDefautFieldMapping("boolean", ""))
	m.DefaultMapping.AddFieldMappingsAt(searchquery.QualifierBoolError, newDefautFieldMapping("boolean", ""))
	m.DefaultMapping.AddFieldMappingsAt(searchquery.QualifierFilterDuration, newDefautFieldMapping("number", ""))
	m.DefaultMapping.AddFieldMappingsAt(searchquery.QualifierFilterStartDate, newDefautFieldMapping("datetime", ""))
	m.DefaultMapping.AddFieldMappingsAt(searchquery.QualifierFilterCompleteDate, newDefautFieldMapping("datetime", ""))

	return m
}

func openIndex(indexBasePath, orgID string) (bleve.Index, error) {
	runtimeConfig := map[string]any{"bolt_timeout": "10s"}
	indexPath := path.Join(indexBasePath, orgID)
	if fi, _ := os.Stat(indexPath); fi != nil && fi.IsDir() {
		return bleve.OpenUsing(indexPath, runtimeConfig)
	}
	return bleve.NewUsing(indexPath, newSessionMapping(), scorch.Name, scorch.Name, runtimeConfig)
}

type Indexer struct {
	idx   bleve.Index
	orgID string
}

var mutexIndexer = map[string]*Indexer{}
var openMutex = sync.RWMutex{}

// Open returns an session indexer scoped to an organization.
// If the index is already opened by an organization, it will
// return the same indexer
func Open(orgID string) (*Indexer, error) {
	openMutex.Lock()
	defer openMutex.Unlock()
	if indexer, ok := mutexIndexer[orgID]; ok {
		return indexer, nil
	}
	index, err := openIndex(PluginIndexPath, orgID)
	if err != nil {
		return nil, err
	}
	indexer := &Indexer{orgID: orgID, idx: index}
	mutexIndexer[orgID] = indexer
	return indexer, nil
}

func (i *Indexer) Index(sessionID string, data any) error {
	return i.idx.Index(sessionID, data)
}

func (i *Indexer) Delete(sessionID string) error {
	return i.idx.Delete(sessionID)
}

func (i *Indexer) Close() error {
	openMutex.Lock()
	defer openMutex.Unlock()
	delete(mutexIndexer, i.orgID)
	return i.idx.Close()
}

func (i *Indexer) Has(sessionID string) (bool, error) {
	doc, err := i.idx.Document(sessionID)
	if err != nil || doc != nil {
		return true, err
	}
	return false, nil
}

func (i *Indexer) Search(req *bleve.SearchRequest) (*bleve.SearchResult, error) {
	ctx, cancelFn := context.WithCancel(context.Background())
	go func() {
		time.Sleep(time.Second * 5)
		cancelFn()
	}()
	return i.idx.SearchInContext(ctx, req)
}
