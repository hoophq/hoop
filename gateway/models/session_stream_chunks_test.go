package models

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConcatJSONArrays(t *testing.T) {
	for _, tt := range []struct {
		msg   string
		parts []string
		want  string
	}{
		{"empty blob, one chunk", []string{`[]`, `[[1, "i", "YQ=="]]`}, `[[1, "i", "YQ=="]]`},
		{"blob then chunks", []string{`[[1, "i", "YQ=="]]`, `[[2, "o", "Yg=="]]`, `[[3, "e", "Yw=="], [4, "i", "ZA=="]]`},
			`[[1, "i", "YQ=="],[2, "o", "Yg=="],[3, "e", "Yw=="], [4, "i", "ZA=="]]`},
		{"empty parts are skipped", []string{`[]`, ` [ ] `, `[1]`, `[]`}, `[1]`},
		{"all empty", []string{`[]`, `[]`}, `[]`},
	} {
		t.Run(tt.msg, func(t *testing.T) {
			parts := make([]json.RawMessage, len(tt.parts))
			for i, p := range tt.parts {
				parts[i] = json.RawMessage(p)
			}
			got, err := concatJSONArrays(parts)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
			assert.True(t, json.Valid(got))
		})
	}

	for _, bad := range []string{``, `{}`, `"[]"`, `[`} {
		_, err := concatJSONArrays([]json.RawMessage{json.RawMessage(`[]`), json.RawMessage(bad)})
		assert.Error(t, err, "%q is not an array", bad)
	}
}
