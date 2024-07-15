package version

import (
	"encoding/json"
	"fmt"
	"runtime"
)

// Info contains versioning information.
type Info struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	Compiler  string `json:"compiler"`
	Platform  string `json:"platform"`
}

var (
	version   string
	gitCommit = "$Format:%H$"          // sha1 from git, output of $(git rev-parse HEAD)
	buildDate = "1970-01-01T00:00:00Z" // build date in ISO8601 format, output of $(date -u +'%Y-%m-%dT%H:%M:%SZ')
	strictTLS = "true"
)

// Get returns the overall codebase version. It's for detecting
// what code a binary was built from.
func Get() Info {
	i := Info{
		Version:   version,
		GitCommit: gitCommit,
		BuildDate: buildDate,
		GoVersion: runtime.Version(),
		Compiler:  runtime.Compiler,
		Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
	if i.Version == "" {
		i.Version = "unknown"
		i.BuildDate = ""
		i.GitCommit = ""
	}
	return i
}

func Decode(obj interface{}) *Info {
	var i Info
	switch v := obj.(type) {
	case string:
		json.Unmarshal([]byte(v), &i)
	case []byte:
		json.Unmarshal(v, &i)
	}
	return &i
}

// JSON returns the version information in JSON format
func JSON() []byte {
	data, _ := json.Marshal(Get())
	return data
}
