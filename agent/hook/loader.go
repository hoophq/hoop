package hook

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type PluginPlatform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

type PluginManifest struct {
	Name      string         `json:"name"`
	Version   string         `json:"version"`
	Size      int            `json:"size"`
	Platform  PluginPlatform `json:"platform"`
	Digest    string         `json:"digest"`
	URL       string         `json:"url"`
	CreatedAt time.Time      `json:"created_at"`

	execPath        string
	info            fs.FileInfo
	isAlreadyLoaded bool
}

func (m *PluginManifest) ExecFilePath() string {
	return filepath.Join(m.execPath, m.Name)
}

func (p *PluginManifest) matchPlatform() bool {
	pp := p.Platform
	return strings.ToLower(pp.Architecture) == runtime.GOARCH &&
		strings.ToLower(pp.OS) == runtime.GOOS
}

func (m *PluginManifest) String() string {
	out := fmt.Sprintf("cached=%v,path=%v", m.isAlreadyLoaded, m.execPath)
	if m.info != nil {
		out = out + ",mode=" + m.info.Mode().String()
	}
	if m.URL != "" {
		return fmt.Sprintf("name=%s,version=%s,size=%v,digest=%s...,platform=%v/%v,%v",
			m.Name, m.Version, m.Size, m.Digest[:15],
			m.Platform.OS, m.Platform.Architecture,
			out)
	}
	return out
}

type versionSection map[string][]PluginManifest
type packageManifest map[string]versionSection

func LoadFromLocalPath(pluginName, pluginExecPath string) (*PluginManifest, error) {
	pm := &PluginManifest{Name: pluginName, execPath: pluginExecPath}
	info, err := os.Stat(pm.ExecFilePath())
	if err != nil {
		return nil, fmt.Errorf("failed loading plugin executable, err=%v", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("failed loading plugin, source is a directoy")
	}
	return pm, nil
}

func LoadFromRegistry(pluginRegistryURL, pluginPath, pkgName string) (*PluginManifest, error) {
	if pluginRegistryURL == "" {
		return nil, fmt.Errorf("plugin registry url is missing")
	}
	if info, _ := os.Stat(pluginPath); info != nil && !info.IsDir() {
		return nil, fmt.Errorf("plugin path %v already exists and isn't a directory", pluginPath)
	}
	pm, err := fetchPluginManifest(pluginRegistryURL, pkgName)
	if err != nil {
		return nil, err
	}

	pm.execPath = filepath.Join(pluginPath, pkgName, pm.Version)
	if isAlreadyLoaded(pm.Digest, filepath.Join(pm.execPath, pm.Name)) {
		pm.isAlreadyLoaded = true
		return pm, nil
	}

	resp, err := http.Get(pm.URL)
	if err != nil {
		return nil, fmt.Errorf("failed fetching plugin source, manifest=%v, err=%v",
			pm, err)
	}
	defer resp.Body.Close()
	_ = os.MkdirAll(pm.execPath, 0755)
	if err := untar(resp.Body, pm.execPath); err != nil {
		return nil, err
	}
	return pm, nil
}

func computeDigest(filePath string) []byte {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	hash := sha256.Sum256(data)
	return append([]byte("sha256:"), hex.EncodeToString(hash[:])...)
}

func isAlreadyLoaded(newPluginDigest string, pluginExecPath string) bool {
	oldPluginDigest := computeDigest(pluginExecPath)
	if oldPluginDigest == nil {
		return false
	}
	return newPluginDigest == string(oldPluginDigest)
}

func fetchPluginManifest(pluginRegistryURL, pkgName string) (*PluginManifest, error) {
	var packagesManifest packageManifest
	resp, err := http.Get(pluginRegistryURL)
	if err != nil {
		return nil, fmt.Errorf("failed fetching plugin registry, err=%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed fetching plugin registry, code=%v", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&packagesManifest); err != nil {
		return nil, fmt.Errorf("failed decoding plugin registry manifest, err=%v", err)
	}

	pkgAttr := packagesManifest[pkgName]
	if pkgAttr == nil {
		return nil, fmt.Errorf("package %v not found on manifest", pkgName)
	}
	versionsList := pkgAttr["versions"]
	if versionsList == nil {
		return nil, fmt.Errorf("missing versions attribute on package manifest for %v", pkgName)
	}
	if len(versionsList) <= 0 {
		return nil, fmt.Errorf("empty versions for package %v", pkgName)
	}
	var pm *PluginManifest
	for _, v := range versionsList {
		if v.matchPlatform() {
			pm = &v
			break
		}
	}
	if pm == nil {
		return nil, fmt.Errorf("couldn't find a plugin that matches the agent platform %s/%s",
			runtime.GOOS, runtime.GOARCH)
	}
	return pm, nil
}

func untar(reader io.Reader, dst string) error {
	gzr, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		switch {
		// no more files
		case err == io.EOF:
			return nil
		case err != nil:
			return err
		case header == nil:
			continue
		}
		target := filepath.Join(dst, header.Name)

		switch header.Typeflag {
		// create directory if doesn't exit
		case tar.TypeDir:
			if _, err := os.Stat(target); err != nil {
				if err := os.MkdirAll(target, 0755); err != nil {
					return err
				}
			}
		// create file
		case tar.TypeReg:
			f, err := os.OpenFile(target, os.O_CREATE|os.O_RDWR, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			defer f.Close()

			// copy contents to file
			if _, err := io.Copy(f, tr); err != nil {
				return err
			}
		}
	}
}
