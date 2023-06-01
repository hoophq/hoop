package main

import (
	"fmt"
	"os"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsimple"
	"github.com/zclconf/go-cty/cty"
)

type PolicyConfig struct {
	Items []Policy `hcl:"policy,block"`
}

// https://github.com/hashicorp/hcl/tree/e54a1960efd6cdfe35ecb8cc098bed33cd6001a8/guide
// https://github.com/hashicorp/hcl/blob/e54a1960efd6cdfe35ecb8cc098bed33cd6001a8/guide/go_patterns.rst#L17
// https://github.com/hashicorp/hcl/blob/e54a1960efd6cdfe35ecb8cc098bed33cd6001a8/gohcl/doc.go#L23
type Policy struct {
	Name              string   `hcl:"name,label"`
	Engine            string   `hcl:"engine"`
	PluginConfigEntry string   `hcl:"plugin_config_entry"`
	Instances         []string `hcl:"instances"`
	RenewDuration     string   `hcl:"renew,optional"`
	GrantPrivileges   []string `hcl:"grant_privileges"`

	datasource string
}

func main() {
	// parser := hclparse.NewParser()
	// parser.ParseHCL()
	c := PolicyConfig{}
	data, _ := os.ReadFile("../../../../../agent/dcm/config.hcl")
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
	fmt.Printf("%#v\n", c)
}
