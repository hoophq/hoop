package memory

import (
	"testing"

	"github.com/google/uuid"
)

func TestConcurrentMapWrites(t *testing.T) {
	s := New()
	for i := 0; i < 50; i++ {
		key := uuid.NewString()
		go s.Del(key)
		go s.Set(key, "foo")
		go s.Get(key)
		go s.Filter(func(_ string) bool { return true })
	}
}
