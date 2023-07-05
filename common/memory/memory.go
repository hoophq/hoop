package memory

import (
	"sync"
)

type Store interface {
	Get(key string) any
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
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return s.m
}

func (s *store) Filter(fn func(key string) bool) map[string]any {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	filteredStore := map[string]any{}
	for key, obj := range s.m {
		if fn(key) {
			filteredStore[key] = obj
		}
	}
	return filteredStore
}

func (s *store) Get(key string) any {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	val, ok := s.m[key]
	if ok {
		return val
	}
	return nil
}

func (s *store) Pop(key string) any {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
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
