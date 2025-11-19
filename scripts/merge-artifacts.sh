#!/bin/bash
set -eu

mkdir -p dist

# Merge all dist-artifacts-* directories into dist/
for artifact_dir in dist/dist-artifacts-*; do
  if [ -d "$artifact_dir" ]; then
    echo "Processing $artifact_dir..."
    echo "  Checking contents..."
    ls -la "$artifact_dir" || true
    # Try to copy if it looks like it contains binaries
    if [ -d "$artifact_dir/binaries" ]; then
      mkdir -p dist/binaries
      cp -r "$artifact_dir/binaries"/* dist/binaries/ 2>/dev/null || true
    fi
    if [ -d "$artifact_dir/webapp.tar.gz" ]; then
      # copy the webapp.tar.gz to dist/
      cp -r "$artifact_dir/webapp.tar.gz" dist/ 2>/dev/null || true
    fi
  fi
done
