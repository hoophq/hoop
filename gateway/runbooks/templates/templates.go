package templates

import (
	"fmt"
	"io"
	"regexp"
	"strings"
	ttemplate "text/template"
	"text/template/parse"

	"github.com/go-git/go-git/v5/plumbing/object"
)

var regexpMatchInputKey = regexp.MustCompile(`\.[a-zA-Z0-9_]+`)

type template struct {
	textTmpl   *ttemplate.Template
	attributes map[string]any
	fnHandler  *templateFnHandler
}

func (t *template) Execute(wr io.Writer, inputs map[string]string) error {
	var missingKeys []string
	for inputKey := range t.attributes {
		if _, ok := inputs[inputKey]; !ok {
			missingKeys = append(missingKeys, inputKey)
		}
	}
	if len(missingKeys) > 0 {
		return fmt.Errorf("the following inputs are missing %v", missingKeys)
	}
	return t.textTmpl.Execute(wr, inputs)
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

func parseSpecFromTemplate(node parse.Node, into map[string]any) error {
	if node.Type() == parse.NodeAction {
		findings := regexpMatchInputKey.FindAllString(node.String(), -1)
		if len(findings) != 1 {
			return fmt.Errorf("inconsistent findings, findings=%v, val=%v", len(findings), findings)
		}
		inputKey := strings.TrimSpace(findings[0])
		inputKey = inputKey[1:] // remove dot
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
			fnVal = strings.Trim(fnVal, `"`)
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
		case "description", "default":
			specs[fnName] = fnVal
		}
	}
	return specs
}

func IsTemplateFile(pathPrefix, filePath string) bool {
	if pathPrefix != "" && !strings.HasPrefix(filePath, pathPrefix) {
		return false
	}
	parts := strings.Split(filePath, "/")
	fileName := parts[len(parts)-1]
	return strings.Contains(fileName, ".template")
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
