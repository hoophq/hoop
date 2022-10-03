package exec

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

func (e *EnvVar) GetKeyVal() string {
	return fmt.Sprintf("%s=%s", e.Key, string(e.Val))
}

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

func (s *EnvVarStore) ParseToKeyVal() []string {
	var keyValList []string
	for _, env := range s.store {
		keyValList = append(keyValList, env.GetKeyVal())
	}
	return keyValList
}

func (s *EnvVarStore) Add(env *EnvVar) {
	s.store[env.Key] = env
}

func newEnvVarStore(rawEnvVarList map[string]interface{}) (*EnvVarStore, error) {
	store := &EnvVarStore{store: make(map[string]*EnvVar)}
	for key, objVal := range rawEnvVarList {
		if key == "xt/id" {
			continue
		}
		key = strings.Replace(key, "secret/", "", 1)
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

// expandEnvVarToCmd expand environment variables contained in the envStore
// and return the list of expanded commands
func expandEnvVarToCmd(envStore *EnvVarStore, cmdList []string) ([]string, error) {
	if envStore == nil {
		envStore = &EnvVarStore{}
	}

	var nonExpandedCmdList []string
	var expandedCmdList []string
	for _, cmd := range cmdList {
		expandedCmd := os.Expand(cmd, envStore.Getenv)
		emptyExpandedEnv := os.Expand(cmd, func(string) string { return "" })
		if expandedCmd == emptyExpandedEnv && strings.Contains(cmd, "$") {
			nonExpandedCmdList = append(nonExpandedCmdList, cmd)
			continue
		}
		expandedCmdList = append(expandedCmdList, expandedCmd)
	}
	if len(nonExpandedCmdList) > 0 {
		return nil, fmt.Errorf("could not find environment variable for commands [%v]",
			strings.Join(nonExpandedCmdList, ","))
	}
	return expandedCmdList, nil
}
