package models

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The approval binds to these bytes and no others. The sidecar's analyzer cache
// key (sqlCacheKey, sidecar/analyzer/content.go) strips literals for the
// opposite reason: two statements of one shape are worth classifying once. A
// key of that shape here would let an approval of `id = 1` release `id = 999`.
func TestHashStatementSeparatesStatementsSharingAShape(t *testing.T) {
	one := HashStatement([]byte("DELETE FROM users WHERE id = 1;"))
	other := HashStatement([]byte("DELETE FROM users WHERE id = 999;"))

	assert.NotEqual(t, one, other, "one approval would release both statements")
	assert.Len(t, one, 64, "the column is CHAR(64) and a shorter value would be padded")
}

// The same bytes must hash alike, or a retry files a second review and the
// approval waiting on the first is never reached.
func TestHashStatementIsStableAndByteExact(t *testing.T) {
	assert.Equal(t, HashStatement([]byte("SELECT 1;")), HashStatement([]byte("SELECT 1;")))

	// Case and whitespace are part of the statement. Normalizing either would
	// widen what a single approval releases.
	assert.NotEqual(t, HashStatement([]byte("SELECT 1;")), HashStatement([]byte("select 1;")),
		"case folding would widen the approval")
	assert.NotEqual(t, HashStatement([]byte("SELECT 1;")), HashStatement([]byte("SELECT  1;")),
		"whitespace folding would widen the approval")
}
