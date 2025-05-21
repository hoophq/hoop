package runbooks

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestTemplateParse(t *testing.T) {
	for _, tt := range []struct {
		msg      string
		tmpl     string
		wantTmpl string
		inputs   map[string]string
		parseErr error
		execErr  error
	}{
		{
			msg:      "it should parse a simple template without inputs or attributes",
			tmpl:     `SELECT id, firstname, lastname FROM customers WHERE id = 10`,
			wantTmpl: `SELECT id, firstname, lastname FROM customers WHERE id = 10`,
		},
		{
			msg: "it should parse a template with multiple inputs without funcs",
			tmpl: `#!/usr/bin/env python3
			data = {
				'amount': {{ .amount00 }},
				'wallet_id': {{ .wallet_id }},
				'debug': {{ .DEBUG_me }}
			}`,
			wantTmpl: `#!/usr/bin/env python3
			data = {
				'amount': 10.4,
				'wallet_id': 59842,
				'debug': True
			}`,
			inputs: map[string]string{
				"amount00": "10.4", "wallet_id": "59842",
				"DEBUG_me": "True", "additional_key_causes_noop": "foo",
			},
		},
		{
			msg:      "it should return an error when the template does not comply with the syntax",
			tmpl:     `{{ .mynput }`,
			inputs:   map[string]string{"mynput": "val"},
			parseErr: fmt.Errorf(`template: :1: unexpected "}" in operand`),
		},
		{
			msg:     "it should return an error if it's missing input keys",
			tmpl:    `SELECT id, firstname, lastname FROM customers WHERE id = {{ .id }}`,
			inputs:  map[string]string{"input_id": ""},
			execErr: fmt.Errorf("the following inputs are missing [id]"),
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			tmpl, err := Parse(tt.tmpl)
			if err != nil {
				if tt.parseErr != nil && tt.parseErr.Error() == err.Error() {
					return
				}
				t.Fatalf("parse error=%v", err)
			}
			got := bytes.NewBufferString("")
			if err := tmpl.Execute(got, tt.inputs); err != nil {
				if tt.execErr != nil && tt.execErr.Error() == err.Error() {
					return
				}
				t.Errorf("exec template error=%v", err)
			}
			if tt.wantTmpl != "" && tt.wantTmpl != got.String() {
				t.Errorf("template does not match, want=%v, got=%v", tt.wantTmpl, got)
			}
		})
	}
}

func TestTemplateParseAttributes(t *testing.T) {
	for _, tt := range []struct {
		msg       string
		tmpl      string
		wantAttrs map[string]any
	}{
		{
			msg:       "it should match a simple name attribute",
			tmpl:      `name = {{ .name }}`,
			wantAttrs: map[string]any{"name": map[string]any{"description": "", "required": false, "type": "text"}},
		},
		{
			msg:  "it should match multiple simple attributes",
			tmpl: `firstname = {{ .firstname }}, lastname = {{ .lastname}}`,
			wantAttrs: map[string]any{
				"firstname": map[string]any{"description": "", "required": false, "type": "text"},
				"lastname":  map[string]any{"description": "", "required": false, "type": "text"},
			},
		},
		{
			msg:  "it should match multiple simple attributes",
			tmpl: `firstname = {{ .firstname }}, lastname = {{ .lastname}}`,
			wantAttrs: map[string]any{
				"firstname": map[string]any{"description": "", "required": false, "type": "text"},
				"lastname":  map[string]any{"description": "", "required": false, "type": "text"},
			},
		},
		{
			msg: "it should match [default, required, description, type, asenv] attributes",
			tmpl: `url = {{ .url 
					| default "http://localhost:3000"
					| required "url is required"
					| description "The URL to fetch"
					| type "url"
					| asenv "FETCH_URL" }}`,
			wantAttrs: map[string]any{
				"url": map[string]any{
					"default":     "http://localhost:3000",
					"required":    true,
					"description": "The URL to fetch",
					"type":        "url",
					"asenv":       "FETCH_URL",
				},
			},
		},
		{
			msg: "it should match [placeholder, type, options] attributes",
			tmpl: `SELECT * FROM gender = {{ .gender
											| placeholder "the gender type"
											| options "male" "female" "unknown"
											| type "select" }}`,
			wantAttrs: map[string]any{
				"gender": map[string]any{
					"description": "",
					"required":    false,
					"placeholder": "the gender type",
					"options":     []string{"male", "female", "unknown"},
					"type":        "select",
				},
			},
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			tmpl, err := Parse(tt.tmpl)
			if err != nil {
				t.Fatalf("parse error=%v", err)
			}
			got := tmpl.Attributes()
			if fmt.Sprintf("%v", tt.wantAttrs) != fmt.Sprintf("%v", got) {
				t.Errorf("attributes doesn't match, want=%v, got=%v", tt.wantAttrs, got)
			}
		})
	}
}

func TestTemplateParseHelperFuncs(t *testing.T) {
	for _, tt := range []struct {
		msg      string
		tmpl     string
		wantTmpl string
		inputs   map[string]string
		execErr  error
	}{
		{
			msg:      "it should parse with a default value if the input attribute is empty",
			tmpl:     `name = {{ .name | default "Tony Stark" }}`,
			wantTmpl: "name = Tony Stark",
			inputs:   map[string]string{"name": ""},
		},
		{
			msg:      "options func should pass the input properly",
			tmpl:     `plan = {{ .plan | default "pro" | options "basic" "pro" "enterprise" | type "select" }}`,
			wantTmpl: "plan = basic",
			inputs:   map[string]string{"plan": "basic"},
		},
		{
			msg:      "options func should must receive the default value func",
			tmpl:     `plan = {{ .plan | default "pro" | options "basic" "pro" "enterprise" | type "select" }}`,
			wantTmpl: "plan = pro",
			inputs:   map[string]string{},
		},
		{
			msg:      "it should parse with a default value if the input is empty",
			tmpl:     `name = {{ .name | default "Tony Stark" }}`,
			wantTmpl: `name = Tony Stark`,
			inputs:   map[string]string{},
		},
		{
			msg:      "it should parse with a default value if the input is empty and using noop functions",
			tmpl:     `name = {{ .name | options "Peter Parker" | default "Tony Stark" }}`,
			wantTmpl: "name = Tony Stark",
			inputs:   map[string]string{"name": ""},
		},
		{
			msg:      "it should put the values in single and double quotes",
			tmpl:     `firstname = {{ .firstname |squote }}, lastname = {{ .lastname |dquote }}`,
			wantTmpl: `firstname = 'Tony', lastname = "Stark"`,
			inputs:   map[string]string{"firstname": "Tony", "lastname": "Stark"},
		},
		{
			msg:      "it should encode and decode inputs as base64",
			tmpl:     `urlenc = {{ .url | encodeb64 }}, urldec = {{ .url_enc | decodeb64 }}`,
			wantTmpl: `urlenc = aHR0cHM6Ly9hcGkuZm9vLnRsZA==, urldec = https://api.foo.tld`,
			inputs:   map[string]string{"url": "https://api.foo.tld", "url_enc": "aHR0cHM6Ly9hcGkuZm9vLnRsZA=="},
		},
		{
			msg:      "it should pass validating the number regexp pattern",
			tmpl:     `wallet_id = {{ .wallet_id | pattern "^[0-9]+" }}`,
			wantTmpl: `wallet_id = 1234567890`,
			inputs:   map[string]string{"wallet_id": "1234567890"},
		},
		{
			msg:      "it should render the template when using options function",
			tmpl:     `type = {{ .type | options "house" "car" "boat" }}`,
			wantTmpl: `type = car`,
			inputs:   map[string]string{"type": "car"},
		},
		{
			msg:     "it should fail validating the number regexp pattern",
			tmpl:    `wallet_id = {{ .wallet_id | pattern "^[0-9]+" }}`,
			inputs:  map[string]string{"wallet_id": "abc1234567890"},
			execErr: fmt.Errorf("pattern didn't match:^[0-9]+"),
		},
		{
			msg:     "it should return error if required attribute is empty",
			tmpl:    `SELECT id, firstname, lastname FROM customers WHERE id = {{ .id | required "id is required" }}`,
			execErr: fmt.Errorf("id is required"),
			inputs:  map[string]string{"id": ""},
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			tmpl, err := Parse(tt.tmpl)
			if err != nil {
				t.Fatalf("parse error=%v", err)
			}
			got := bytes.NewBufferString("")
			if err := tmpl.Execute(got, tt.inputs); err != nil {
				if tt.execErr != nil && strings.Contains(err.Error(), tt.execErr.Error()) {
					return
				}
				t.Fatalf("exec template error=%v", err)
			}
			if tt.wantTmpl != "" && tt.wantTmpl != got.String() {
				t.Errorf("template does not match, want=%v, got=%v", tt.wantTmpl, got)
			}
		})
	}
}

func TestIsRunbookFile(t *testing.T) {
	for _, tt := range []struct {
		msg        string
		filePath   string
		pathPrefix string
		want       bool
	}{
		{
			msg:      "it must match file with runbook on its name",
			filePath: "team/finops/dba/charge.runbook.sql",
			want:     true,
		},
		{
			msg:      "it must not match when there is not a runbook name",
			filePath: "team/finops/dba/charge.runb.sql",
			want:     false,
		},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			got := IsRunbookFile(tt.filePath)
			if tt.want != got {
				t.Errorf("runbook file validation fail, want=%v, got=%v", tt.want, got)
			}
		})
	}
}
