package policy

import "sync"

// ConnState holds one value per key for one connection. An evaluator keys
// its value by itself, so a lane's two evaluators keep apart.
type ConnState struct {
	m sync.Map
}

// Value returns the value stored under key, storing newValue's first.
func (s *ConnState) Value(key any, newValue func() any) any {
	if v, ok := s.m.Load(key); ok {
		return v
	}
	v, _ := s.m.LoadOrStore(key, newValue())
	return v
}
