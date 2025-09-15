package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecPath(t *testing.T) {
	t.Run("should return executable path", func(t *testing.T) {
		path, err := execPath()
		require.NoError(t, err)
		assert.NotEmpty(t, path)
		assert.True(t, filepath.IsAbs(path))
	})

	t.Run("should resolve symlinks", func(t *testing.T) {
		path, err := execPath()
		require.NoError(t, err)

		resolved, err := filepath.EvalSymlinks(path)
		require.NoError(t, err)
		assert.Equal(t, resolved, path)
	})
}

func TestEnvFileAlreadyExist(t *testing.T) {
	t.Run("should return true for existing file", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test_env_file")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())
		tmpFile.Close()

		result := envFileAlreadyExist(tmpFile.Name())
		assert.True(t, result)
	})

	t.Run("should return false for non-existing file", func(t *testing.T) {
		result := envFileAlreadyExist("/non/existing/file")
		assert.False(t, result)
	})

	t.Run("should return false for directory", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "test_dir")
		require.NoError(t, err)
		defer os.RemoveAll(tmpDir)

		result := envFileAlreadyExist(tmpDir)
		assert.True(t, result)
	})
}

func TestEnvFileExist(t *testing.T) {
	t.Run("should return path when env file exists", func(t *testing.T) {
		tmpHome, err := os.MkdirTemp("", "test_home")
		require.NoError(t, err)
		defer os.RemoveAll(tmpHome)

		configDir := filepath.Join(tmpHome, ".config")
		err = os.MkdirAll(configDir, 0755)
		require.NoError(t, err)

		envFile := filepath.Join(configDir, "hoop.conf")
		err = os.WriteFile(envFile, []byte("test=value"), 0644)
		require.NoError(t, err)

		// Mock os.UserHomeDir to return our temp directory
		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tmpHome)
		defer os.Setenv("HOME", originalHome)

		path, err := envFileExist()
		require.NoError(t, err)
		assert.Equal(t, envFile, path)
	})

	t.Run("should return error when env file does not exist", func(t *testing.T) {
		tmpHome, err := os.MkdirTemp("", "test_home")
		require.NoError(t, err)
		defer os.RemoveAll(tmpHome)

		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tmpHome)
		defer os.Setenv("HOME", originalHome)

		path, err := envFileExist()
		assert.Error(t, err)
		assert.Empty(t, path)
		assert.Contains(t, err.Error(), "env file not found")
	})
}

func TestLoadEnvFile(t *testing.T) {
	t.Run("should load valid env file", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test_env")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		content := `# This is a comment
KEY1=value1
KEY2=value2
KEY3=value with spaces
KEY4=~/relative/path
# Another comment
KEY5=value5
`
		err = os.WriteFile(tmpFile.Name(), []byte(content), 0644)
		require.NoError(t, err)

		env, err := LoadEnvFile(tmpFile.Name())
		require.NoError(t, err)

		expected := map[string]string{
			"KEY1": "value1",
			"KEY2": "value2",
			"KEY3": "value with spaces",
			"KEY4": filepath.Join(os.Getenv("HOME"), "relative/path"),
			"KEY5": "value5",
		}

		assert.Equal(t, expected, env)
	})

	t.Run("should handle empty file", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test_env")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		env, err := LoadEnvFile(tmpFile.Name())
		require.NoError(t, err)
		assert.Empty(t, env)
	})

	t.Run("should skip malformed lines", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test_env")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		content := `VALID=value
MALFORMED
ANOTHER=valid
=empty_key
KEY=`
		err = os.WriteFile(tmpFile.Name(), []byte(content), 0644)
		require.NoError(t, err)

		env, err := LoadEnvFile(tmpFile.Name())
		require.NoError(t, err)

		expected := map[string]string{
			"":        "empty_key",
			"ANOTHER": "valid",
			"KEY":     "",
			"VALID":   "value",
		}

		assert.Equal(t, expected, env)
	})

	t.Run("should return error for non-existing file", func(t *testing.T) {
		env, err := LoadEnvFile("/non/existing/file")
		assert.Error(t, err)
		assert.Nil(t, env)
	})

	t.Run("should handle scanner error", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test_env")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		_, err = tmpFile.WriteString("test=value\n")
		require.NoError(t, err)
		tmpFile.Close()

		env, err := LoadEnvFile(tmpFile.Name())
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"test": "value"}, env)
	})
}

func TestCreateEnvFileIfNotExists(t *testing.T) {
	t.Run("should create env file with given content", func(t *testing.T) {
		tmpHome, err := os.MkdirTemp("", "test_home")
		require.NoError(t, err)
		defer os.RemoveAll(tmpHome)

		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tmpHome)
		defer os.Setenv("HOME", originalHome)

		env := map[string]string{
			"KEY1": "value1",
			"KEY2": "value2",
		}

		path, err := createEnvFileIfNotExists(env)
		require.NoError(t, err)

		expectedPath := filepath.Join(tmpHome, ".config", "hoop.conf")
		assert.Equal(t, expectedPath, path)

		content, err := os.ReadFile(path)
		require.NoError(t, err)

		expectedContent := "KEY1=value1\nKEY2=value2\n"
		assert.Equal(t, expectedContent, string(content))

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	})

	t.Run("should create .config directory if it doesn't exist", func(t *testing.T) {
		tmpHome, err := os.MkdirTemp("", "test_home")
		require.NoError(t, err)
		defer os.RemoveAll(tmpHome)

		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tmpHome)
		defer os.Setenv("HOME", originalHome)

		env := map[string]string{"KEY": "value"}

		path, err := createEnvFileIfNotExists(env)
		require.NoError(t, err)

		configDir := filepath.Join(tmpHome, ".config")
		_, err = os.Stat(configDir)
		require.NoError(t, err)

		_, err = os.Stat(path)
		require.NoError(t, err)
	})

	t.Run("should return error if unable to create directory", func(t *testing.T) {
		env := map[string]string{"KEY": "value"}

		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", "/root/restricted")
		defer os.Setenv("HOME", originalHome)

		path, err := createEnvFileIfNotExists(env)
		assert.Error(t, err)
		assert.Empty(t, path)
	})
}

func TestWriteFileIfNotExists(t *testing.T) {
	t.Run("should write file when it doesn't exist", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test_write")
		require.NoError(t, err)
		tmpFile.Close()
		os.Remove(tmpFile.Name()) // Remove the file so it doesn't exist

		content := "test content"
		err = writeFileIfNotExists(tmpFile.Name(), content, 0644)
		require.NoError(t, err)

		actualContent, err := os.ReadFile(tmpFile.Name())
		require.NoError(t, err)
		assert.Equal(t, content, string(actualContent))

		// Clean up
		os.Remove(tmpFile.Name())
	})

	t.Run("should not overwrite existing file", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "test_write")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())

		originalContent := "original content"
		err = os.WriteFile(tmpFile.Name(), []byte(originalContent), 0644)
		require.NoError(t, err)

		newContent := "new content"
		err = writeFileIfNotExists(tmpFile.Name(), newContent, 0644)
		require.NoError(t, err)

		actualContent, err := os.ReadFile(tmpFile.Name())
		require.NoError(t, err)
		assert.Equal(t, originalContent, string(actualContent))
	})

	t.Run("should return error if unable to write file", func(t *testing.T) {
		invalidPath := "/root/restricted/file"
		content := "test content"

		err := writeFileIfNotExists(invalidPath, content, 0644)
		assert.Error(t, err)
	})

}

func TestConfigEnvironmentVariables(t *testing.T) {
	t.Run("should use existing env file when available", func(t *testing.T) {
		tmpHome, err := os.MkdirTemp("", "test_home")
		require.NoError(t, err)
		defer os.RemoveAll(tmpHome)

		configDir := filepath.Join(tmpHome, ".config")
		err = os.MkdirAll(configDir, 0755)
		require.NoError(t, err)

		envFile := filepath.Join(configDir, "hoop.conf")
		envContent := `HOOP_KEY=test_token
CUSTOM_VAR=custom_value
`
		err = os.WriteFile(envFile, []byte(envContent), 0644)
		require.NoError(t, err)

		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tmpHome)
		defer os.Setenv("HOME", originalHome)

		originalHoopKey := os.Getenv("HOOP_KEY")
		os.Setenv("HOOP_KEY", "https://test-agent:test-secret@example.com:443?mode=standard")
		defer os.Setenv("HOOP_KEY", originalHoopKey)

		env, err := configEnvironmentVariables()
		require.NoError(t, err)

		expected := map[string]string{
			"HOOP_KEY":   "test_token",
			"CUSTOM_VAR": "custom_value",
		}

		assert.Equal(t, expected, env)
	})

	t.Run("should create new env file when none exists", func(t *testing.T) {
		tmpHome, err := os.MkdirTemp("", "test_home")
		require.NoError(t, err)
		defer os.RemoveAll(tmpHome)

		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tmpHome)
		defer os.Setenv("HOME", originalHome)

		originalHoopKey := os.Getenv("HOOP_KEY")
		os.Setenv("HOOP_KEY", "https://test-agent:test-secret@example.com:443?mode=standard")
		defer os.Setenv("HOOP_KEY", originalHoopKey)

		// Mock os.Getenv for PATH
		originalPath := os.Getenv("PATH")
		os.Setenv("PATH", "/usr/bin:/bin")
		defer os.Setenv("PATH", originalPath)

		env, err := configEnvironmentVariables()
		require.NoError(t, err)

		expected := map[string]string{
			"HOOP_KEY": "https://test-agent:test-secret@example.com:443?mode=standard",
			"PATH":     "/usr/bin:/bin",
		}

		assert.Equal(t, expected, env)

		envFile := filepath.Join(tmpHome, ".config", "hoop.conf")
		_, err = os.Stat(envFile)
		require.NoError(t, err)
	})

	t.Run("should return error when agentconfig.Load fails", func(t *testing.T) {
		originalHoopKey := os.Getenv("HOOP_KEY")
		os.Unsetenv("HOOP_KEY")
		defer os.Setenv("HOOP_KEY", originalHoopKey)

		env, err := configEnvironmentVariables()
		assert.Error(t, err)
		assert.Nil(t, env)
	})

	t.Run("should return error when unable to create env file", func(t *testing.T) {
		tmpHome, err := os.MkdirTemp("", "test_home")
		require.NoError(t, err)
		defer os.RemoveAll(tmpHome)

		originalHome := os.Getenv("HOME")
		os.Setenv("HOME", tmpHome)
		defer os.Setenv("HOME", originalHome)

		originalHoopKey := os.Getenv("HOOP_KEY")
		os.Setenv("HOOP_KEY", "https://test-agent:test-secret@example.com:443?mode=standard")
		defer os.Setenv("HOOP_KEY", originalHoopKey)

		configDir := filepath.Join(tmpHome, ".config")
		err = os.WriteFile(configDir, []byte("blocking file"), 0644)
		require.NoError(t, err)

		env, err := configEnvironmentVariables()
		assert.Error(t, err)
		assert.Nil(t, env)
	})
}

func assertFileContent(t *testing.T, filePath, expectedContent string) {
	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, expectedContent, string(content))
}

func assertFileExistsWithPerms(t *testing.T, filePath string, expectedPerm os.FileMode) {
	info, err := os.Stat(filePath)
	require.NoError(t, err)
	assert.Equal(t, expectedPerm, info.Mode().Perm())
}
