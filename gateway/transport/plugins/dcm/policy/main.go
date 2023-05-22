package main

import (
	"fmt"
	"os"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsimple"
	"github.com/zclconf/go-cty/cty"
)

type Config struct {
	Items []Database `hcl:"database,block"`
}

// https://github.com/hashicorp/hcl/tree/e54a1960efd6cdfe35ecb8cc098bed33cd6001a8/guide
// https://github.com/hashicorp/hcl/blob/e54a1960efd6cdfe35ecb8cc098bed33cd6001a8/guide/go_patterns.rst#L17
// https://github.com/hashicorp/hcl/blob/e54a1960efd6cdfe35ecb8cc098bed33cd6001a8/gohcl/doc.go#L23
type Database struct {
	// Foo                  string   `hcl:"foo"`
	Host                 string   `hcl:"host,label"`
	Name                 string   `hcl:"name,label"`
	Engine               string   `hcl:"engine"`
	PostgresSchemas      []string `hcl:"postgres_schemas,optional"`
	OnCreateStatements   []string `hcl:"on_create"`
	OnExistentStatements []string `hcl:"on_existent"`
}

func main() {
	// parser := hclparse.NewParser()
	// parser.ParseHCL()
	c := Config{}
	data, _ := os.ReadFile("./policies.hcl")
	variables := map[string]cty.Value{
		"user":       cty.StringVal("master-user"),
		"password":   cty.StringVal("master-pwd"),
		"expiration": cty.StringVal("12h"),
		// ""
	}
	err := hclsimple.Decode("policies.hcl", data, &hcl.EvalContext{Variables: variables}, &c)
	if err != nil {
		panic(err)
	}
	fmt.Println(c)
}
