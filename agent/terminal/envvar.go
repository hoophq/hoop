package terminal

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
)

type (
	EnvVar struct {
		Key        string
		Type       string
		Val        []byte
		OnPreExec  func() error
		OnPostExec func() error
	}
	EnvVarStore struct {
		store map[string]*EnvVar
	}
)

func (e *EnvVar) GetDecodedVal() ([]byte, error) {
	return base64.StdEncoding.DecodeString(string(e.Val))
}

func (s *EnvVarStore) Getenv(key string) string {
	env := s.store[key]
	if env == nil {
		return ""
	}
	return string(env.Val)
}

func (s *EnvVarStore) Add(env *EnvVar) {
	s.store[env.Key] = env
}

func (s *EnvVarStore) Search(lookupFn func(key string) bool) map[string]string {
	result := map[string]string{}
	for key, env := range s.store {
		if !lookupFn(key) {
			continue
		}
		result[key] = string(env.Val)
	}
	return result
}

func NewEnvVarStore(rawEnvVarList map[string]any) (*EnvVarStore, error) {
	store := &EnvVarStore{store: make(map[string]*EnvVar)}
	for key, objVal := range rawEnvVarList {
		keyType := strings.Split(key, ":")
		if len(keyType) != 2 {
			return nil, fmt.Errorf("environment variable key type in unknown format, want=[keytype:key], got=%v", key)
		}
		encVal, ok := objVal.(string)
		if !ok {
			return nil, fmt.Errorf("decoding env var error, expected string, found=%T", objVal)
		}
		env := &EnvVar{Type: keyType[0], Key: keyType[1], Val: []byte(encVal)}
		switch env.Type {
		case "filesystem":
			val, err := env.GetDecodedVal()
			if err != nil {
				return nil, fmt.Errorf("failed decoding env var %q, err=%v", env.Key, err)
			}
			filePath := fmt.Sprintf("/tmp/%s.envfs", uuid.NewString())
			env.OnPreExec = func() error {
				f, err := os.Create(filePath)
				if err != nil {
					return fmt.Errorf("failed creating temp file for %v, err=%v", env.Key, err)
				}
				if err := f.Chmod(0600); err != nil {
					return fmt.Errorf("failed changing permission of env var file, err=%v", err)
				}
				_, err = f.Write(val)
				return err
			}
			env.OnPostExec = func() error { return os.Remove(filePath) }
			env.Val = []byte(filePath)
		case "envvar":
			val, err := env.GetDecodedVal()
			if err != nil {
				return nil, fmt.Errorf("failed decoding env var %q, err=%v", env.Key, err)
			}
			env.Val = []byte(val)
		default:
			return nil, fmt.Errorf(`unknow environment variable type %q, accepted: "filesystem", "envvar"`, keyType[0])
		}
		store.Add(env)
	}
	return store, nil
}
