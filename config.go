package bb

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Config holds everything needed to talk to the Bitbucket Cloud API, plus local preferences.
type Config struct {
	// Workspace is the Bitbucket workspace slug (the <workspace> in bitbucket.org/<workspace>/<repo>).
	Workspace string `json:"workspace"`
	Email     string `json:"email"`
	Token     string `json:"token"`
	// Home is the local directory repositories are cloned into by default. Not to be confused
	// with Workspace (a Bitbucket slug) or the user's $HOME: it is a path on disk, and optional.
	Home string `json:"home,omitempty"`
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate user config dir: %w", err)
	}
	return filepath.Join(dir, "bb", "config.json"), nil
}

// loadConfig reads the stored config. A missing file yields an empty Config.
func loadConfig() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cfg, nil
}

// requireConfig loads the config and fails if `bb configure` hasn't been completed.
func requireConfig() (*Config, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, err
	}
	if cfg.Workspace == "" || cfg.Email == "" || cfg.Token == "" {
		return nil, errors.New("bb is not configured: run `bb configure` first")
	}
	return cfg, nil
}

// requireHome returns the configured local home directory, or an error telling the
// user to configure one or use alternative (a description of what to do instead).
func (c *Config) requireHome(alternative string) (string, error) {
	if c.Home == "" {
		return "", fmt.Errorf("home directory is not configured: run `bb configure` to set one, %s", alternative)
	}
	return c.Home, nil
}

// normalizeHome expands a leading ~ and makes the path absolute. An empty path stays
// empty (meaning "not configured"). It fails if the path exists but isn't a directory.
func normalizeHome(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", nil
	}

	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		p = filepath.Join(home, strings.TrimPrefix(p, "~"))
	}

	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		return "", fmt.Errorf("%s exists and is not a directory", abs)
	}
	return abs, nil
}

// saveConfig writes the config with owner-only permissions, since it contains a token.
func saveConfig(cfg *Config) (string, error) {
	path, err := configPath()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}

	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write config: %w", err)
	}
	// WriteFile only applies the mode when creating the file.
	if err := os.Chmod(path, 0o600); err != nil {
		return "", fmt.Errorf("chmod config: %w", err)
	}
	return path, nil
}
