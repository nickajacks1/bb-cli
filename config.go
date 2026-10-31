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

// configOption describes one named setting for `bb configure get/set`.
type configOption struct {
	name   string
	secret bool // masked by `get` unless --unsafe-show-secrets is given
	// optional options may be cleared by setting an empty value.
	optional bool
	field    func(*Config) *string
	// normalize validates and canonicalizes a value before it is stored; nil means trim only.
	normalize func(string) (string, error)
}

var configOptions = []configOption{
	{name: "workspace", field: func(c *Config) *string { return &c.Workspace }},
	{name: "email", field: func(c *Config) *string { return &c.Email }},
	{name: "token", secret: true, field: func(c *Config) *string { return &c.Token }},
	{name: "home", optional: true, field: func(c *Config) *string { return &c.Home }, normalize: normalizeHome},
}

func configOptionNames() []string {
	names := make([]string, len(configOptions))
	for i, o := range configOptions {
		names[i] = o.name
	}
	return names
}

// lookupConfigOption finds an option by name, listing the valid names when it is unknown.
func lookupConfigOption(name string) (*configOption, error) {
	for i := range configOptions {
		if configOptions[i].name == name {
			return &configOptions[i], nil
		}
	}
	return nil, fmt.Errorf("unknown config option %q: valid options are %s", name, strings.Join(configOptionNames(), ", "))
}

// get returns the option's current value, failing with a hint when it is unset.
func (o *configOption) get(c *Config) (string, error) {
	v := *o.field(c)
	if v == "" {
		return "", fmt.Errorf("%s is not set: run `bb configure` or `bb configure set %s <value>`", o.name, o.name)
	}
	return v, nil
}

// display returns v as it should be printed: secrets are masked unless showSecrets is set.
// An unset (empty) value is never masked.
func (o *configOption) display(v string, showSecrets bool) string {
	if o.secret && v != "" && !showSecrets {
		return "********"
	}
	return v
}

// set validates value and stores it in c.
func (o *configOption) set(c *Config, value string) error {
	value = strings.TrimSpace(value)
	if value == "" && !o.optional {
		return fmt.Errorf("%s cannot be empty", o.name)
	}
	if o.normalize != nil {
		var err error
		if value, err = o.normalize(value); err != nil {
			return err
		}
	}
	*o.field(c) = value
	return nil
}

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
