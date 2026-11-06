package runbooks

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateParseKeepsSecretTypeOnRepeatedInput(t *testing.T) {
	for _, typ := range []string{"password", "file"} {
		for _, tmpl := range []string{
			`a={{ .secret | type "` + typ + `" }} b={{ .secret }}`,
			`a={{ .secret }} b={{ .secret | type "` + typ + `" }}`,
		} {
			p, err := Parse(tmpl)
			require.NoError(t, err)
			attr, _ := p.Attributes()["secret"].(map[string]any)
			assert.Equal(t, typ, attr["type"], tmpl)
		}
	}
	// a later typed reference still wins over a text one
	p, err := Parse(`{{ .n | type "text" }} {{ .n | type "number" }}`)
	require.NoError(t, err)
	assert.Equal(t, "number", p.Attributes()["n"].(map[string]any)["type"])
}
