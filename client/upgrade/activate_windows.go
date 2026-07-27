//go:build windows

package upgrade

// activate points the bin path at the installed binary. On Windows,
// unprivileged symlink creation is not available by default, so the active
// version is published as a copy at $HOME\.hoop\bin\hoop.exe.
func activate(l Layout, store *Store, target string) error {
	return copyActivate(l, store, target)
}
