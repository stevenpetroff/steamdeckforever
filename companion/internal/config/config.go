// Package config loads and saves the deckchat service config file
// (~/.config/deckchat/config.toml).
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// DefaultPort is the HTTP/WebSocket port used when the config doesn't set one.
const DefaultPort = 7480

// Config is the on-disk service configuration.
type Config struct {
	Port    int    `toml:"port"`
	WoWPath string `toml:"wow_path"` // optional override for the _forever_ directory
}

// Default returns the configuration used when no file exists.
func Default() Config { return Config{Port: DefaultPort} }

// DefaultPath returns the config file location under the given home directory.
func DefaultPath(home string) string {
	return filepath.Join(home, ".config", "deckchat", "config.toml")
}

// Validate reports whether cfg holds usable values.
func (c Config) Validate() error {
	if c.Port < 1024 || c.Port > 65535 {
		return fmt.Errorf("config: port %d out of range 1024-65535", c.Port)
	}
	return nil
}

// Load reads the config at path. A missing file yields the defaults.
// Keys absent from the file keep their default values.
func Load(path string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("%w (in %s)", err, path)
	}
	return cfg, nil
}

// Save writes cfg to path atomically (temp file + rename) with mode 0600,
// creating the parent directory with mode 0700 if needed.
func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(cfg); err != nil {
		return fmt.Errorf("config: encode: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: create dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return fmt.Errorf("config: create temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	// CreateTemp already uses 0600; Chmod makes it explicit regardless of umask.
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("config: chmod temp: %w", err)
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return fmt.Errorf("config: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("config: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("config: rename: %w", err)
	}
	return nil
}
