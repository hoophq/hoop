package memory

import (
	"sync"
	// "github.com/runopsio/runops-proxy/internal/types"
)

type Store interface {
	Get(key string) interface{}
	Set(key string, val interface{})
	Del(key string)
	List() map[string]interface{}
	Filter(func(key string) bool) map[string]interface{}
}

type store struct {
	mutex sync.RWMutex
	m     map[string]interface{}
}

func (s *store) Set(key string, val interface{}) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.m[key] = val
}

func (s *store) Del(key string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.m, key)
}

func (s *store) List() map[string]interface{} {
	return s.m
}

func (s *store) Filter(fn func(key string) bool) map[string]interface{} {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	filteredStore := map[string]interface{}{}
	for key, obj := range s.m {
		if fn(key) {
			filteredStore[key] = obj
		}
	}
	return filteredStore
}

func (s *store) Get(key string) interface{} {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	val, ok := s.m[key]
	if ok {
		return val
	}
	return nil
}

func New() Store {
	return &store{
		m:     map[string]interface{}{},
		mutex: sync.RWMutex{},
	}
}
