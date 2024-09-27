package webappjs

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/hoophq/hoop/common/log"
	"github.com/hoophq/hoop/common/version"
	"github.com/hoophq/hoop/gateway/appconfig"
)

const (
	hardcodedWebappApiURL       string = "http://localhost:8009"
	hardcodedWebappAssetsURL    string = "http://localhost:8280"
	hardcodedWebappAppJsVersion string = "?version=unknown"
	hardcodedWebappBaseRoute    string = "/hardcoded-runtime-prefix"
)

var appVersion = fmt.Sprintf("?version=%v", version.Get().Version)

func ConfigureServerURL() error {
	apiURL := appconfig.Get().ApiURL()
	staticUiPath := appconfig.Get().WebappStaticUiPath()
	if apiURL == "" || staticUiPath == "" {
		return fmt.Errorf("api url and static ui path are not set")
	}
	apiURL = apiURL + appconfig.Get().ApiURLPath()
	baseRoutePrefix := appconfig.Get().ApiURLPath()
	if baseRoutePrefix == "/" {
		baseRoutePrefix = ""
	}
	if err := replaceWebappURLAppJsFile(apiURL, staticUiPath, baseRoutePrefix); err != nil {
		return err
	}
	if err := replaceWebappURLIndexFile(apiURL, staticUiPath); err != nil {
		return err
	}
	return nil
}

func replaceWebappURLIndexFile(apiURL, staticUiPath string) error {
	indexFile := filepath.Join(staticUiPath, "index.html")
	indexFileOrigin := filepath.Join(staticUiPath, "index.origin.html")
	// use the copy of index file if exists
	if fileBytes, err := os.ReadFile(indexFileOrigin); err == nil {
		log.Infof("replacing api url from origin at %v with %v", indexFile, apiURL)
		fileBytes = bytes.ReplaceAll(fileBytes, []byte(hardcodedWebappAssetsURL), []byte(apiURL))
		fileBytes = bytes.ReplaceAll(fileBytes, []byte(hardcodedWebappAppJsVersion), []byte(appVersion))
		if err := os.WriteFile(indexFile, fileBytes, 0644); err != nil {
			return fmt.Errorf("failed saving index.html file, reason=%v", err)
		}
		return nil
	}
	fileBytes, err := os.ReadFile(indexFile)
	if err != nil {
		return fmt.Errorf("failed opening index.html file %v, reason=%v", indexFile, err)
	}
	// create a copy to allow overriding the api url
	if err := os.WriteFile(indexFileOrigin, fileBytes, 0644); err != nil {
		return fmt.Errorf("failed creating index.origin.html copy file at %v, reason=%v", indexFileOrigin, err)
	}

	log.Infof("replacing api url at %v with %v", indexFile, apiURL)
	fileBytes = bytes.ReplaceAll(fileBytes, []byte(hardcodedWebappAssetsURL), []byte(apiURL))
	fileBytes = bytes.ReplaceAll(fileBytes, []byte(hardcodedWebappAppJsVersion), []byte(appVersion))
	if err := os.WriteFile(indexFile, fileBytes, 0644); err != nil {
		return fmt.Errorf("failed saving index.html file, reason=%v", err)
	}
	return nil
}

func replaceWebappURLAppJsFile(apiURL, staticUiPath, baseRoutePrefix string) error {
	appJsFile := filepath.Join(staticUiPath, "js/app.js")
	appJsFileOrigin := filepath.Join(staticUiPath, "js/app.origin.js")
	// use the copy js file if exists
	if appBytes, err := os.ReadFile(appJsFileOrigin); err == nil {
		log.Infof("replacing api url from origin at %v with url=%v, base-route-prefix=%q",
			appJsFile, apiURL, baseRoutePrefix)
		appBytes = bytes.ReplaceAll(appBytes, []byte(hardcodedWebappApiURL), []byte(apiURL))
		appBytes = bytes.ReplaceAll(appBytes, []byte(hardcodedWebappAssetsURL), []byte(apiURL))
		appBytes = bytes.ReplaceAll(appBytes, []byte(hardcodedWebappBaseRoute), []byte(baseRoutePrefix))
		if err := os.WriteFile(appJsFile, appBytes, 0644); err != nil {
			return fmt.Errorf("failed saving app.js file, reason=%v", err)
		}
		return nil
	}
	appBytes, err := os.ReadFile(appJsFile)
	if err != nil {
		return fmt.Errorf("failed opening webapp js file %v, reason=%v", appJsFile, err)
	}
	// create a copy to allow overriding the api url
	if err := os.WriteFile(appJsFileOrigin, appBytes, 0644); err != nil {
		return fmt.Errorf("failed creating app.origin.js copy file at %v, reason=%v", appJsFileOrigin, err)
	}

	log.Infof("replacing api url at %v with url=%v, base-route-prefix=%q",
		appJsFile, apiURL, baseRoutePrefix)
	appBytes = bytes.ReplaceAll(appBytes, []byte(hardcodedWebappApiURL), []byte(apiURL))
	appBytes = bytes.ReplaceAll(appBytes, []byte(hardcodedWebappAssetsURL), []byte(apiURL))
	appBytes = bytes.ReplaceAll(appBytes, []byte(hardcodedWebappBaseRoute), []byte(baseRoutePrefix))
	if err := os.WriteFile(appJsFile, appBytes, 0644); err != nil {
		return fmt.Errorf("failed saving app.js file, reason=%v", err)
	}
	return nil
}
