package apiagents

import (
	"strings"
	"testing"

	apiconnections "github.com/hoophq/hoop/gateway/api/connections"
	"github.com/hoophq/hoop/gateway/api/openapi"
	"github.com/hoophq/hoop/gateway/models"
	"github.com/stretchr/testify/assert"
)

// The predefined agent tags keep the values of the connection system tags
// under the plain label: hoop.dev/infrastructure.environment becomes environment.
func TestPredefinedTagsAreConnectionSystemTagsWithoutPrefix(t *testing.T) {
	systemTags := map[string][]string{}
	for _, tag := range apiconnections.DefaultConnectionTags("org") {
		label := tag.Key[strings.LastIndex(tag.Key, ".")+1:]
		systemTags[label] = append(systemTags[label], tag.Value)
	}
	for key, values := range predefinedTags {
		assert.False(t, strings.HasPrefix(key, "hoop.dev/"), "agent tags must not expose the internal prefix: %s", key)
		assert.Contains(t, systemTags, key)
		assert.ElementsMatch(t, systemTags[key], values, key)
	}
}

func TestTagSuggestions(t *testing.T) {
	items := tagSuggestions([]models.AgentTag{
		{Key: "environment", Value: "prod"},
		{Key: "business-unit", Value: "payments"},
	})
	var count int
	for _, values := range predefinedTags {
		count += len(values)
	}
	// prod is predefined: it is not repeated.
	assert.Len(t, items, count+1)
	assert.Equal(t, openapi.AgentTag{Key: "business-unit", Value: "payments"}, items[0])
	for i := 1; i < len(items); i++ {
		prev, cur := items[i-1], items[i]
		assert.True(t, prev.Key < cur.Key || (prev.Key == cur.Key && prev.Value < cur.Value), "not sorted at %d", i)
	}
	assert.NotNil(t, tagSuggestions(nil))
}
