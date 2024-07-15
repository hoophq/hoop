package envloader

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
)

const (
	base64EnvLoader string = "base64://"
	fileEnvLoader   string = "file://"
)

// GetEnv loads a environment variable based on the value type
//
// base64://<base64-enc-val> - decodes the base64 value using base64.StdEncoding
//
// file://<path/to/file> - loads based on the relative or absolute path
//
// If none of the above prefixes are found it returns the value from os.Getenv
func GetEnv(key string) (string, error) {
	v := os.Getenv(key)
	switch {
	case strings.HasPrefix(v, base64EnvLoader):
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, base64EnvLoader))
		if err != nil {
			return "", err
		}
		v = string(data)
	case strings.HasPrefix(v, fileEnvLoader):
		filePath := strings.TrimPrefix(v, fileEnvLoader)
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
		v = string(data)
	}
	return v, nil
}
