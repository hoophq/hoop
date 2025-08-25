package daemon

import (
	"fmt"
	"os"
	"path/filepath"
)

func execPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("get executable path: %w", err)
	}

	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("resolve executable symlinks: %w", err)
	}
	return exe, nil
}

