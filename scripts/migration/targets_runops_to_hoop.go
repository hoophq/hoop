package main

import (
	"encoding/csv"
	"fmt"
	"os"
)

// this script requires a CSV file
// containing all the targets to be migrated
//
// csv MUST be SEMI-COLON separated (;) because
// the "reviewers" field are a comma separated
// field that will mess the parsing

// required fields in this order are:
// [name, type, review_type, reviewers, redact]
//
// select name,type,review_type,reviewers,redact
// from targets where org = '{}' and status = 'active'
// order by name;

type target struct {
	name       string
	tp         string
	reviewType string
	reviewers  string
	redact     string
}

func main() {
	if len(os.Args) <= 1 {
		fmt.Println("Must provide an argument with the CSV path")
		os.Exit(1)
	}
	csvPath := os.Args[1]
	file, err := os.Open(csvPath)
	if err != nil {
		fmt.Printf("Failed to load CSV file, err=%v\n", err)
		os.Exit(1)
	}
	defer file.Close()

	r := csv.NewReader(file)
	r.FieldsPerRecord = -1
	r.Comma = ';'

	records, err := r.ReadAll()
	if err != nil {
		fmt.Printf("Failed to read CSV records, err=%v\n", err)
		os.Exit(1)
	}

	targets := buildTargets(records)
	fmt.Println(targets[0])
}

func buildTargets(records [][]string) []target {
	targets := make([]target, 0)
	for i, row := range records {
		if i == 0 {
			continue
		}
		targets = append(targets, target{
			name:       row[0],
			tp:         row[1],
			reviewType: row[2],
			reviewers:  row[3],
			redact:     row[4],
		})
	}
	return targets
}
