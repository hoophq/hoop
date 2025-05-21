package runbooks

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	ttemplate "text/template"
	"text/template/parse"

	"github.com/go-git/go-git/v5/plumbing/object"
)

type template struct {
	textTmpl   *ttemplate.Template
	attributes map[string]any
	fnHandler  *templateFnHandler
}

func (t *template) Execute(wr io.Writer, inputs map[string]string) error {
	execInputs := map[string]string{}
	for inputKey, inputVal := range inputs {
		execInputs[inputKey] = inputVal
	}

	var missingKeys []string
	for inputKey, obj := range t.attributes {
		if _, ok := execInputs[inputKey]; !ok {
			metadata, _ := obj.(map[string]any)
			if metadata != nil {
				// if it has a default attribute and it's non empty
				// add as a entry key to render the default value from the template
				if defaultVal, _ := metadata["default"].(string); defaultVal != "" {
					execInputs[inputKey] = ""
					continue
				}
			}
			missingKeys = append(missingKeys, inputKey)
		}
	}
	if len(missingKeys) > 0 {
		return fmt.Errorf("the following inputs are missing %v", missingKeys)
	}
	return t.textTmpl.Execute(wr, execInputs)
}

func (t *template) Attributes() map[string]any {
	return t.attributes
}

func (t *template) EnvVars() map[string]string {
	return t.fnHandler.items
}

func Parse(tmpl string) (*template, error) {
	t := &template{
		fnHandler:  &templateFnHandler{items: map[string]string{}},
		attributes: map[string]any{},
	}
	funcs := defaultStaticTemplateFuncs()
	funcs["asenv"] = t.fnHandler.AsEnv
	textTempl, err := ttemplate.New("").Funcs(funcs).Parse(tmpl)
	if err != nil {
		return nil, err
	}
	t.textTmpl = textTempl
	if err := parseSpecFromTemplate(t.textTmpl.Tree.Root, t.attributes); err != nil {
		return nil, err
	}
	// t.Option("missingkey=invalid")
	return t, nil
}

var regexpMatchInputKey = regexp.MustCompile(`^{{\.[a-zA-Z0-9_]+`)

func parseSpecFromTemplate(node parse.Node, into map[string]any) error {
	if node.Type() == parse.NodeAction {
		findings := regexpMatchInputKey.FindAllString(node.String(), -1)
		if len(findings) != 1 {
			return fmt.Errorf("inconsistent findings, findings=%v, val=%v", len(findings), findings)
		}
		inputKey := strings.TrimSpace(findings[0])
		inputKey = inputKey[3:] // remove prefix {{.
		into[inputKey] = parseNode(node.String())
	}
	if ln, ok := node.(*parse.ListNode); ok {
		for _, n := range ln.Nodes {
			if err := parseSpecFromTemplate(n, into); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseNode parse a node structure
// {{ .mykey | myfn "arg" | myfn02 | myfn03 }}
func parseNode(node string) map[string]any {
	specs := map[string]any{
		"type":        "text",
		"required":    false,
		"description": "",
	}

	// strip start/end template
	node = node[2:]
	node = node[:len(node)-2]
	parts := strings.Split(node, " | ")
	for _, section := range parts {
		sectionsParts := strings.Split(section, " ")
		fnName := sectionsParts[0]
		fnVal := ""
		if len(sectionsParts) > 1 {
			fnVal = strings.TrimSpace(
				strings.Join(sectionsParts[1:], " "))
			fnVal = strings.ReplaceAll(fnVal, `"`, "")
		}
		switch fnName {
		case "type":
			for _, key := range defaultInputTypes {
				if key == fnVal {
					specs[fnName] = fnVal
					break
				}
			}
		case "required":
			specs[fnName] = true
		case "description", "default", "placeholder":
			specs[fnName] = fnVal
		case "options":
			specs[fnName] = strings.Split(fnVal, " ")
		case "asenv":
			specs[fnName] = fnVal
		}
	}
	return specs
}

// IsRunbookFile checks if the filePath contains '.runbook.' in its name
func IsRunbookFile(filePath string) bool {
	parts := strings.Split(filePath, "/")
	fileName := parts[len(parts)-1]
	return strings.Contains(fileName, ".runbook.")
}

func LookupFile(fileName string, t *object.Tree) *object.File {
	var objFile *object.File
	t.Files().ForEach(func(f *object.File) error {
		if f.Name == fileName {
			objFile = f
		}
		return nil
	})
	return objFile
}

func ReadBlob(f *object.File) ([]byte, error) {
	reader, err := f.Blob.Reader()
	if err != nil {
		return nil, fmt.Errorf("reader error %v", err)
	}
	return io.ReadAll(reader)
}
