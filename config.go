package bb

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Config holds everything needed to talk to the Bitbucket Cloud API.
type Config struct {
	Workspace string `json:"workspace"`
	Email     string `json:"email"`
	Token     string `json:"token"`
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
