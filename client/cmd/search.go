package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"

	"github.com/blevesearch/bleve/v2"
	"github.com/runopsio/hoop/client/cmd/styles"
	clientconfig "github.com/runopsio/hoop/client/config"
	"github.com/runopsio/hoop/gateway/indexer"
	"github.com/runopsio/hoop/gateway/indexer/searchquery"
	"github.com/spf13/cobra"
)

const searchApiURI = "/api/plugins/indexer/sessions/search"

var markResultsFlag bool
var limitFlag int
var offsetFlag int
var fieldsFlag []string
var facetsFlag []string

// searchCmd represents the exec command
var searchCmd = &cobra.Command{
	Use:   "search QUERY",
	Short: "Search for content in sessions",
	PreRun: func(cmd *cobra.Command, args []string) {
		if len(args) < 1 {
			cmd.Usage()
			os.Exit(1)
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		runSearch(args[0])
	},
}

func init() {
	searchCmd.Flags().BoolVarP(&markResultsFlag, "mark", "m", false, "Highlight results")
	searchCmd.Flags().StringSliceVar(&fieldsFlag, "fields", nil, "The fields to display")
	searchCmd.Flags().StringSliceVar(&facetsFlag, "facets", nil, "The facets to display, [connection,connection_type,user,error,verb,duration]")
	searchCmd.Flags().IntVarP(&limitFlag, "limit", "l", 50, "The max results to return")
	searchCmd.Flags().IntVarP(&offsetFlag, "offset", "o", 0, "The offset to paginate results")
	rootCmd.AddCommand(searchCmd)
}

func runSearch(inputQuery string) {
	config := clientconfig.GetClientConfigOrDie()
	req := indexer.NewSearchRequest(inputQuery, limitFlag, offsetFlag, "ansi")
	if len(fieldsFlag) > 0 {
		req.Fields = fieldsFlag
	}
	setDefaultFacets(req)
	result, err := searchHTTPRequest(config, req)
	if err != nil {
		printErrorAndExit(err.Error())
	}
	fmt.Println(&searchResult{result})
}

func searchHTTPRequest(c *clientconfig.Config, searchRequest any) (*bleve.SearchResult, error) {
	body, err := json.Marshal(searchRequest)
	if err != nil {
		return nil, fmt.Errorf("failed marshaling body request, err=%v", err)
	}
	apiURL := fmt.Sprintf("%s%s", c.ApiURL, searchApiURI)
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", c.Token))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed performing search request, err=%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed searching, status-code=%v, payload=%v", resp.StatusCode, string(data))
	}
	var searchResult bleve.SearchResult
	return &searchResult, json.NewDecoder(resp.Body).Decode(&searchResult)
}

func setDefaultFacets(req *indexer.SearchRequest) {
	for _, facetkey := range facetsFlag {
		switch facetkey {
		case searchquery.QualifierFilterConnection:
			req.AddFacet("connections", bleve.NewFacetRequest(searchquery.QualifierFilterConnection, 5))
		case searchquery.QualifierFilterConnectionType:
			req.AddFacet("types", bleve.NewFacetRequest(searchquery.QualifierFilterConnectionType, 5))
		case searchquery.QualifierFilterUser:
			req.AddFacet("users", bleve.NewFacetRequest(searchquery.QualifierFilterUser, 5))
		case searchquery.QualifierBoolError:
			req.AddFacet("errors", bleve.NewFacetRequest(searchquery.QualifierBoolError, 2))
		case searchquery.QualifierFilterVerb:
			req.AddFacet("verbs", bleve.NewFacetRequest(searchquery.QualifierFilterVerb, 2))
		case searchquery.QualifierFilterDuration:
			durationf := bleve.NewFacetRequest(searchquery.QualifierFilterDuration, 3)
			fastDuration := 15.0
			normalDuration := 50.0
			slowDuration := 120.0
			durationf.AddNumericRange("< 15sec ", nil, &fastDuration)
			durationf.AddNumericRange("15s..50s ", &fastDuration, &normalDuration)
			durationf.AddNumericRange("> 120sec ", &slowDuration, nil)
			req.AddFacet(searchquery.QualifierFilterDuration, durationf)
		}
	}
}

func printErrorAndExit(format string, v ...any) {
	errOutput := styles.ClientError(fmt.Sprintf(format, v...))
	fmt.Println(errOutput)
	os.Exit(1)
}

type searchResult struct {
	*bleve.SearchResult
}

func (sr *searchResult) String() string {
	rv := ""
	if sr.Total > 0 {
		if sr.Request.Size > 0 {
			rv = fmt.Sprintf("%d matches, showing %d through %d, took %s\n", sr.Total, sr.Request.From+1, sr.Request.From+len(sr.Hits), sr.Took)
			for i, hit := range sr.Hits {
				rv += fmt.Sprintf("%5d. %s (%f)\n", i+sr.Request.From+1, hit.ID, hit.Score)
				var sortedFragmentFields []string
				for fragmentField := range hit.Fragments {
					sortedFragmentFields = append(sortedFragmentFields, fragmentField)
				}
				sort.Strings(sortedFragmentFields)
				for _, fragmentField := range sortedFragmentFields {
					rv += fmt.Sprintf("\t%s\n", fragmentField)
					for _, fragment := range hit.Fragments[fragmentField] {
						rv += fmt.Sprintf("\t\t%v\n", fragment)
					}
				}
				for _, fieldName := range sr.Request.Fields {
					if _, ok := hit.Fragments[fieldName]; ok {
						continue
					}
					fieldValue := hit.Fields[fieldName]
					rv += fmt.Sprintf("\t%s\n", fieldName)
					rv += fmt.Sprintf("\t\t%v\n", fieldValue)
				}
			}
		} else {
			rv = fmt.Sprintf("%d matches, took %s\n", sr.Total, sr.Took)
		}
	} else {
		rv = "No matches"
	}
	if len(sr.Facets) > 0 && rv != "No matches" {
		rv += "\n"
		rv += styles.Keyword(" Facets: ")
		rv += "\n\n"
		var facetFields []string
		for key := range sr.Facets {
			facetFields = append(facetFields, key)
		}
		sort.Strings(facetFields)
		for _, facetkey := range facetFields {
			f := sr.Facets[facetkey]
			rv += fmt.Sprintf("%s(%d)\n", facetkey, f.Total)
			for _, t := range f.Terms.Terms() {
				rv += fmt.Sprintf("\t%s(%d)\n", t.Term, t.Count)
			}
			for _, n := range f.NumericRanges {
				rv += fmt.Sprintf("\t%s(%d)\n", n.Name, n.Count)
			}
			for _, d := range f.DateRanges {
				rv += fmt.Sprintf("\t%s(%d)\n", d.Name, d.Count)
			}
			if f.Other != 0 {
				rv += fmt.Sprintf("\tOther(%d)\n", f.Other)
			}
		}
	}
	return rv
}
