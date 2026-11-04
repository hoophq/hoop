package apirunbooks

import (
	"encoding/json"
	"testing"

	"github.com/hoophq/hoop/common/runbooks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeRequestParamsRedactsSecrets(t *testing.T) {
	rb := &runbooks.File{TemplateAttributes: map[string]any{
		"secret": map[string]any{"type": "password"},
		"cert":   map[string]any{"type": "file"},
		"name":   map[string]any{"type": "text"},
	}}
	params := map[string]string{
		"secret": "s3cr3t-value",
		"cert":   "abcd",
		"name":   "/app/db/password",
		"extra":  "not-in-template",
	}

	var got map[string]string
	require.NoError(t, json.Unmarshal(encodeRequestParams(params, rb), &got))

	assert.Equal(t, map[string]string{
		"secret": "[param redacted. type = password]",
		"cert":   "[param redacted. type = file, size = 4 byte(s)]",
		"name":   "/app/db/password",
		"extra":  "not-in-template",
	}, got)
}

func TestEncodeRequestParamsWithoutAttributes(t *testing.T) {
	var got map[string]string
	require.NoError(t, json.Unmarshal(encodeRequestParams(map[string]string{"a": "b"}, &runbooks.File{}), &got))
	assert.Equal(t, map[string]string{"a": "b"}, got)
}
