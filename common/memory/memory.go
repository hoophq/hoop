package memory

import (
	"sync"
)

type Store interface {
	Get(key string) any
	Has(key string) bool
	Pop(key string) any
	Set(key string, val any)
	Del(key string)
	List() map[string]any
	Filter(func(key string) bool) map[string]any
}

type store struct {
	mutex sync.RWMutex
	m     map[string]any
}

func (s *store) Set(key string, val any) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.m[key] = val
}

func (s *store) Del(key string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	delete(s.m, key)
}

func (s *store) List() map[string]any {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	copyMap := map[string]any{}
	for key, val := range s.m {
		copyMap[key] = val
	}
	return copyMap
}

func (s *store) Filter(fn func(key string) bool) map[string]any {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	filteredStore := map[string]any{}
	for key, obj := range s.m {
		if fn(key) {
			filteredStore[key] = obj
		}
	}
	return filteredStore
}

func (s *store) Get(key string) any {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	val, ok := s.m[key]
	if ok {
		return val
	}
	return nil
}

// Has report if the key is found in the map
func (s *store) Has(key string) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	_, exists := s.m[key]
	return exists
}

func (s *store) Pop(key string) any {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	val, ok := s.m[key]
	if ok {
		delete(s.m, key)
		return val
	}
	return nil
}

func New() Store {
	return &store{
		m:     map[string]any{},
		mutex: sync.RWMutex{},
	}
}
