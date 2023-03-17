package indexer

import (
	"context"
	"errors"
	"fmt"
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

var (
	PluginIndexPath       = os.Getenv("PLUGIN_INDEX_PATH")
	indexFolderTimeFormat = "2006-01-02T15.04.05.999999999Z07.00"
	runtimeConfig         = map[string]any{"bolt_timeout": "10s"}
	stateFileName         = "current-index"
)

func init() {
	if PluginIndexPath == "" {
		PluginIndexPath = defaultPluginPath
	}
}

type updateStateFileFunc func() error

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

func openStateFileIndex(indexBasePath, orgID string) (bleve.Index, error) {
	stateFilePath := path.Join(indexBasePath, orgID, stateFileName)
	currentFileName, err := os.ReadFile(stateFilePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("failed opening state file, err=%v", err)
	}
	if len(currentFileName) > 0 {
		return bleve.OpenUsing(string(currentFileName), runtimeConfig)
	}
	return nil, nil
}

func newBleveIndex(orgID string) (bleve.Index, updateStateFileFunc, error) {
	indexRootPath := path.Join(PluginIndexPath, orgID)
	indexPath := path.Join(indexRootPath, time.Now().UTC().Format(indexFolderTimeFormat))
	if err := os.MkdirAll(indexPath, 0700); err != nil {
		return nil, nil, fmt.Errorf("failed creating index path, err=%v", err)
	}
	indexStateFile := path.Join(indexRootPath, stateFileName)
	updateStateFileFn := func() error {
		f, err := os.OpenFile(indexStateFile, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0744)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteString(indexPath)
		return err
	}
	index, err := bleve.NewUsing(indexPath, newSessionMapping(), scorch.Name, scorch.Name, runtimeConfig)
	return index, updateStateFileFn, err
}

type Indexer struct {
	idx    bleve.IndexAlias
	origin bleve.Index
	orgID  string
	name   string
}

var mutexIndexer = map[string]*Indexer{}
var openMutex = sync.RWMutex{}

// NewIndexer tries to open the last index from the filesystem or
// open a fresh one.
func NewIndexer(orgID string) (*Indexer, error) {
	openMutex.Lock()
	defer openMutex.Unlock()
	if indexer, ok := mutexIndexer[orgID]; ok {
		return indexer, nil
	}
	index, err := openStateFileIndex(PluginIndexPath, orgID)
	if err != nil {
		return nil, err
	}
	indexer := &Indexer{orgID: orgID}
	if index == nil {
		index, updateStateFileFn, err := newBleveIndex(orgID)
		if err != nil {
			return nil, err
		}
		if err := updateStateFileFn(); err != nil {
			return nil, fmt.Errorf("failed updating state file, err=%v", err)
		}
		indexer.name = index.Name()
		indexer.idx = bleve.NewIndexAlias(index)
		indexer.origin = index
		mutexIndexer[orgID] = indexer
		return indexer, nil
	}
	indexer.name = index.Name()
	indexer.origin = index
	indexer.idx = bleve.NewIndexAlias(index)
	mutexIndexer[orgID] = indexer
	return indexer, nil
}

func (i *Indexer) swapIndex(newIndex bleve.Index) error {
	openMutex.Lock()
	defer openMutex.Unlock()
	// swap indexes
	i.name = newIndex.Name()
	i.idx.Remove(i.origin)
	i.idx.Add(newIndex)
	// add the new state to the indexer instance
	oldIndex := i.origin
	i.idx = bleve.NewIndexAlias(newIndex)
	i.origin = newIndex
	// cleanup
	err := oldIndex.Close()
	_ = os.RemoveAll(oldIndex.Name())
	return err
}

func (i *Indexer) Name() string {
	return i.name
}

func (i *Indexer) Index(sessionID string, data any) error {
	return i.idx.Index(sessionID, data)
}

func (i *Indexer) Search(req *bleve.SearchRequest) (*bleve.SearchResult, error) {
	ctx, cancelFn := context.WithTimeout(context.Background(), time.Second*5)
	defer cancelFn()
	return i.idx.SearchInContext(ctx, req)
}

func (i *Indexer) Close() error {
	openMutex.Lock()
	defer openMutex.Unlock()
	delete(mutexIndexer, i.orgID)
	return i.idx.Close()
}
