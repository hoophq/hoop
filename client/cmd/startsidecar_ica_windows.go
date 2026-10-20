//go:build windows

package cmd

import "errors"

// Windows has no exec, and no ica lane either: the engine's OCR worker and
// CIDR mode are Linux. Validate the config on the host that will run it.
func execHoopInspect(string, []string) error {
	return errors.New("an ica lane runs on Linux; validate and run this config there")
}
