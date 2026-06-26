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

// claudeSettings is the resolved set of values written into the Claude Code
// settings env block for a connection. When vertexProject is set the connection
// federates to Google Vertex AI and the Vertex-mode env keys are emitted;
// otherwise the standard Anthropic-mode keys are used.
type claudeSettings struct {
	baseURL       string
	proxyToken    string
	vertexProject string
	vertexRegion  string
}

func (s claudeSettings) isVertex() bool { return s.vertexProject != "" }

// vertexEnvKeys are the env keys that only make sense in Vertex mode. They are
// stripped when writing an Anthropic-mode connection so switching a connection
// back and forth never leaves stale keys behind.
var vertexEnvKeys = []string{
	"CLAUDE_CODE_USE_VERTEX",
	"ANTHROPIC_VERTEX_PROJECT_ID",
	"CLOUD_ML_REGION",
	"ANTHROPIC_VERTEX_BASE_URL",
}

// updateClaudeSettings merges the hoop-managed env keys into
// ~/.claude/settings.json, preserving all other existing keys.
//
//   - Anthropic mode sets ANTHROPIC_BASE_URL (+ ANTHROPIC_CUSTOM_HEADERS when a
//     proxy token is present) and strips any Vertex-mode keys.
//   - Vertex mode sets CLAUDE_CODE_USE_VERTEX, ANTHROPIC_VERTEX_PROJECT_ID,
//     CLOUD_ML_REGION and ANTHROPIC_VERTEX_BASE_URL (so Claude Code talks the
//     Vertex protocol to the hoop proxy) and strips ANTHROPIC_BASE_URL.
//
// In both modes the proxy token rides in ANTHROPIC_CUSTOM_HEADERS as the
// Authorization header; the gateway authenticates it and the agent swaps in the
// real upstream credential.
func updateClaudeSettings(s claudeSettings, settingsFile string) error {
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

	if s.isVertex() {
		delete(env, "ANTHROPIC_BASE_URL")
		env["CLAUDE_CODE_USE_VERTEX"] = "1"
		env["ANTHROPIC_VERTEX_PROJECT_ID"] = s.vertexProject
		env["CLOUD_ML_REGION"] = s.vertexRegion
		env["ANTHROPIC_VERTEX_BASE_URL"] = s.baseURL
	} else {
		for _, k := range vertexEnvKeys {
			delete(env, k)
		}
		env["ANTHROPIC_BASE_URL"] = s.baseURL
	}

	if s.proxyToken != "" {
		env["ANTHROPIC_CUSTOM_HEADERS"] = "Authorization: " + s.proxyToken
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
