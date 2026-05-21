package cmdutils

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	base64UriType string = "base64://"
	fileUriType   string = "file://"
)

// GetEnvValue loads a raw inline value, a base64 inline value or a value from a file
//
// base64://<base64-enc-val> - decodes the base64 value using base64.StdEncoding
//
// file://<path/to/file> - loads based on the relative or absolute path
//
// If none of the above prefixes are found it returns the value as it is
func GetEnvValue(val string) (string, error) {
	switch {
	case strings.HasPrefix(val, base64UriType):
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(val, base64UriType))
		if err != nil {
			return "", err
		}
		return string(data), nil
	case strings.HasPrefix(val, fileUriType):
		filePath := strings.TrimPrefix(val, fileUriType)
		isAbs := strings.HasPrefix(filePath, "/")
		if !isAbs {
			pwdDir, err := os.Getwd()
			if err != nil {
				return "", err
			}
			filePath = filepath.Join(pwdDir, filePath)
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return val, nil
}

func ParseEnvPerType(envPairs []string) (envVar map[string]string, err error) {
	envVar = map[string]string{}
	var invalidEnvs []string
	for _, envvarStr := range envPairs {
		key, val, found := strings.Cut(envvarStr, "=")
		if !found {
			invalidEnvs = append(invalidEnvs, envvarStr)
			continue
		}
		envType, keyenv, found := strings.Cut(key, ":")
		if found {
			key = keyenv
		} else {
			envType = "envvar"
		}
		if envType != "envvar" && envType != "filesystem" {
			return nil, fmt.Errorf("wrong environment type, acecpt one off: (envvar, filesystem)")
		}
		val, err = GetEnvValue(val)
		if err != nil {
			return nil, fmt.Errorf("unable to get value: %v", err)
		}
		key = fmt.Sprintf("%v:%v", envType, key)
		envVar[key] = base64.StdEncoding.EncodeToString([]byte(val))
	}
	if len(invalidEnvs) > 0 {
		return nil, fmt.Errorf("invalid env vars, expected env=var. found=%v", invalidEnvs)
	}
	return envVar, nil
}
