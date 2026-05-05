package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const claudeSettingsPath = ".claude/settings.json"

func claudeSettingsFilePath(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("could not resolve home directory: %w", err)
	}
	return filepath.Join(home, claudeSettingsPath), nil
}

// updateClaudeSettings merges ANTHROPIC_BASE_URL (and optionally ANTHROPIC_CUSTOM_HEADERS)
// into ~/.claude/settings.json, preserving all other existing keys.
// If proxyToken is empty, ANTHROPIC_CUSTOM_HEADERS is removed from the env section.
func updateClaudeSettings(baseURL, proxyToken, settingsFile string) error {
	path, err := claudeSettingsFilePath(settingsFile)
	if err != nil {
		return err
	}

	settings := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if jsonErr := json.Unmarshal(data, &settings); jsonErr != nil {
			return fmt.Errorf("could not parse %s: %w", path, jsonErr)
		}
	}

	env, _ := settings["env"].(map[string]any)
	if env == nil {
		env = map[string]any{}
	}
	env["ANTHROPIC_BASE_URL"] = baseURL
	if proxyToken != "" {
		env["ANTHROPIC_CUSTOM_HEADERS"] = "Authorization: " + proxyToken
	} else {
		delete(env, "ANTHROPIC_CUSTOM_HEADERS")
	}
	settings["env"] = env

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("could not create directory for %s: %w", path, err)
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0600)
}
