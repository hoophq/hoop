//go:build ignore

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func main() {
	// Get the directory where this script is located
	scriptDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get working directory: %v\n", err)
		os.Exit(1)
	}

	// Path to wasm directory
	wasmDir := filepath.Join(scriptDir, "..", "wasm")

	// Build the WASM module
	fmt.Println("Building RDP parser WASM module...")

	cmd := exec.Command("cargo", "build", "--release", "--target", "wasm32-unknown-unknown")
	cmd.Dir = wasmDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build WASM module: %v\n", err)
		os.Exit(1)
	}

	// Copy the WASM file to the parser directory
	srcPath := filepath.Join(wasmDir, "target", "wasm32-unknown-unknown", "release", "rdp_parser.wasm")
	dstPath := filepath.Join(scriptDir, "rdp_parser.wasm")

	// Read source file
	srcData, err := os.ReadFile(srcPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read WASM file: %v\n", err)
		os.Exit(1)
	}

	// Write to destination
	if err := os.WriteFile(dstPath, srcData, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write WASM file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("WASM module built and copied to %s\n", dstPath)
}