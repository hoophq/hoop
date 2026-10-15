package apivalidation

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateTags(t *testing.T) {
	assert.NoError(t, ValidateTags("tags", nil))
	assert.NoError(t, ValidateTags("tags", map[string]string{
		"hoop.dev/infrastructure.environment": "prod",
		"business_unit":                       "Payments EU/West + 1",
		"owner":                               "team@hoop-dev:on=call",
	}))

	tooMany := map[string]string{}
	for i := 0; i <= MaxTags; i++ {
		tooMany[fmt.Sprintf("k%d", i)] = "v"
	}
	assert.EqualError(t, ValidateTags("tags", tooMany), "max tag association reached (10)")

	for _, tc := range []struct {
		key, val string
	}{
		{"", "v"},
		{"key-", "v"},
		{"bad key", "v"},
		{strings.Repeat("k", 65), "v"},
		{"key", ""},
		{"key", "bad#value"},
		// The value rule has no (.), whatever the message says: connections keep it.
		{"key", "v1.2"},
		{"key", strings.Repeat("v", 257)},
	} {
		err := ValidateTags("tags", map[string]string{tc.key: tc.val})
		assert.Error(t, err, "%q=%q", tc.key, tc.val)
	}
	assert.NoError(t, ValidateTags("tags", map[string]string{strings.Repeat("k", 64): strings.Repeat("v", 256)}))
}

func TestTagErrorsNameTheFieldInKeyOrder(t *testing.T) {
	errs := TagErrors("connection_tags", map[string]string{"b!": "v", "a!": "v"})
	if assert.Len(t, errs, 2) {
		assert.True(t, strings.HasPrefix(errs[0], "connection_tags (a!), keys must"), errs[0])
		assert.True(t, strings.HasPrefix(errs[1], "connection_tags (b!), keys must"), errs[1])
	}
}
