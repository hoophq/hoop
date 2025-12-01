package ssmproxy

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestUUID2AWS(t *testing.T) {
	u := uuid.New()
	t.Logf("Generated UUID: %s", u.String())
	accessKey, err := UUIDToAccessKey(u.String())
	assert.NoError(t, err)
	assert.NotEmpty(t, accessKey)
	t.Logf("Converted UUID to access key: %s", accessKey)

	_, err = UUIDToAccessKey("invalid-uuid")
	assert.Error(t, err)

	_, err = UUIDToAccessKey("")
	assert.Error(t, err)

	assert.Equal(t, "AKIA", accessKey[:4])
	u2, err := AccessKeyToUUID(accessKey)
	assert.NoError(t, err)
	assert.Equal(t, u.String(), u2)
}
