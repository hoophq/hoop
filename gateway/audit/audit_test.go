package audit

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewEvent(t *testing.T) {
	evt := NewEvent(ResourceUser, ActionCreate)

	assert.Equal(t, ResourceUser, evt.resourceType)
	assert.Equal(t, ActionCreate, evt.action)
	assert.NotNil(t, evt.payload)
	assert.Empty(t, evt.payload)
	assert.Nil(t, evt.err)
	assert.Empty(t, evt.resourceID)
	assert.Empty(t, evt.resourceName)
}

func TestEventResource(t *testing.T) {
	evt := NewEvent(ResourceConnection, ActionUpdate).
		Resource("abc-123", "my-conn")

	assert.Equal(t, "abc-123", evt.resourceID)
	assert.Equal(t, "my-conn", evt.resourceName)
}

func TestEventResourceOverwrite(t *testing.T) {
	evt := NewEvent(ResourceConnection, ActionCreate).
		Resource("", "my-conn").
		Resource("final-id", "my-conn")

	assert.Equal(t, "final-id", evt.resourceID)
	assert.Equal(t, "my-conn", evt.resourceName)
}

func TestEventSet(t *testing.T) {
	evt := NewEvent(ResourceAgent, ActionCreate).
		Set("name", "agent-1").
		Set("type", "default").
		Set("count", 42)

	assert.Equal(t, "agent-1", evt.payload["name"])
	assert.Equal(t, "default", evt.payload["type"])
	assert.Equal(t, 42, evt.payload["count"])
	assert.Len(t, evt.payload, 3)
}

func TestEventSetOverwritesKey(t *testing.T) {
	evt := NewEvent(ResourceAgent, ActionUpdate).
		Set("name", "old").
		Set("name", "new")

	assert.Equal(t, "new", evt.payload["name"])
	assert.Len(t, evt.payload, 1)
}

func TestEventSetMap(t *testing.T) {
	evt := NewEvent(ResourceUser, ActionCreate).
		Set("existing", "value").
		setMap(map[string]any{
			"key1": "val1",
			"key2": 2,
		})

	assert.Equal(t, "value", evt.payload["existing"])
	assert.Equal(t, "val1", evt.payload["key1"])
	assert.Equal(t, 2, evt.payload["key2"])
	assert.Len(t, evt.payload, 3)
}

func TestEventSetMapOverwritesExisting(t *testing.T) {
	evt := NewEvent(ResourceUser, ActionUpdate).
		Set("name", "old").
		setMap(map[string]any{"name": "new"})

	assert.Equal(t, "new", evt.payload["name"])
}

func TestEventSetStruct(t *testing.T) {
	type user struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		Age   int    `json:"age"`
	}

	evt := NewEvent(ResourceUser, ActionCreate).
		SetStruct(user{Name: "Alice", Email: "alice@example.com", Age: 30})

	assert.Equal(t, "Alice", evt.payload["name"])
	assert.Equal(t, "alice@example.com", evt.payload["email"])
	assert.Equal(t, float64(30), evt.payload["age"]) // JSON numbers are float64
	assert.Len(t, evt.payload, 3)
}

func TestEventSetStructMergesWithExisting(t *testing.T) {
	type info struct {
		Role string `json:"role"`
	}

	evt := NewEvent(ResourceUser, ActionCreate).
		Set("name", "Alice").
		SetStruct(info{Role: "admin"})

	assert.Equal(t, "Alice", evt.payload["name"])
	assert.Equal(t, "admin", evt.payload["role"])
}

func TestEventSetStructUnexportedFieldsIgnored(t *testing.T) {
	type mixed struct {
		Public  string `json:"public"`
		private string //nolint:unused
	}

	evt := NewEvent(ResourceUser, ActionCreate).
		SetStruct(mixed{Public: "visible"})

	assert.Equal(t, "visible", evt.payload["public"])
	assert.Len(t, evt.payload, 1)
}

func TestEventSetStructWithUnmarshalableValue(t *testing.T) {
	// channels cannot be marshaled to JSON
	evt := NewEvent(ResourceUser, ActionCreate).
		Set("before", "ok").
		SetStruct(make(chan int))

	// should silently fail and keep existing payload
	assert.Equal(t, "ok", evt.payload["before"])
	assert.Len(t, evt.payload, 1)
}

func TestEventErr(t *testing.T) {
	evt := NewEvent(ResourceUser, ActionCreate)
	assert.Nil(t, evt.err)

	evt.Err(errors.New("something failed"))
	assert.EqualError(t, evt.err, "something failed")
}

func TestEventErrNil(t *testing.T) {
	evt := NewEvent(ResourceUser, ActionCreate).
		Err(nil)

	assert.Nil(t, evt.err)
}

func TestEventErrOverwrite(t *testing.T) {
	evt := NewEvent(ResourceUser, ActionCreate).
		Err(errors.New("first")).
		Err(errors.New("second"))

	assert.EqualError(t, evt.err, "second")
}

func TestEventChaining(t *testing.T) {
	err := errors.New("db timeout")
	evt := NewEvent(ResourceGuardrails, ActionUpdate).
		Resource("rule-1", "my-rule").
		Set("name", "my-rule").
		Set("description", "blocks selects").
		Set("input", map[string]any{"rules": []string{"deny"}}).
		Err(err)

	assert.Equal(t, ResourceGuardrails, evt.resourceType)
	assert.Equal(t, ActionUpdate, evt.action)
	assert.Equal(t, "rule-1", evt.resourceID)
	assert.Equal(t, "my-rule", evt.resourceName)
	assert.Equal(t, "my-rule", evt.payload["name"])
	assert.Equal(t, "blocks selects", evt.payload["description"])
	assert.NotNil(t, evt.payload["input"])
	assert.Equal(t, err, evt.err)
}
