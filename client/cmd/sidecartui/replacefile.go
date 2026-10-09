package sidecartui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// replaceFile writes data to path through a temporary file in the same
// folder, renamed over path when complete: a crash leaves the old file,
// never half of the new one. A path that is a symlink or not a regular file
// is refused: the folder may be a cloned repository, and its link could
// point at a file the person never meant to replace.
func replaceFile(path string, data []byte, mode fs.FileMode) error {
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return err
	case fi.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("%s is a symbolic link; replace the file it points at yourself", path)
	case !fi.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file", path)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // a no-op once the rename succeeds
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
