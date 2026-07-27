package webappui

import (
	"embed"
	"io/fs"
)

// staticui holds the webapp build embedded into the binary. It is empty in
// the repository (only a .gitkeep placeholder) and populated at release
// build time by `make embed-webapp`, which stages the merged webapp build
// (the public/ tree of dist/webapp.tar.gz) into gateway/webappui/staticui.
//
//go:embed all:staticui
var embeddedAssets embed.FS

// embeddedFS returns the embedded UI rooted at staticui, or nil when the
// binary was built without a webapp build staged (e.g. dev builds).
func embeddedFS() fs.FS {
	sub, err := fs.Sub(embeddedAssets, "staticui")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, indexFileName); err != nil {
		return nil
	}
	return sub
}
