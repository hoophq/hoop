#!/usr/bin/env bash
# PostToolUse hook: auto-format any Go file Claude edits or writes.
# Deterministic — runs on every edit, unlike prose instructions in CLAUDE.md.
file=$(jq -r '.tool_input.file_path // empty' 2>/dev/null)
case "$file" in
  *.go) gofmt -w "$file" 2>/dev/null || true ;;
esac
exit 0
