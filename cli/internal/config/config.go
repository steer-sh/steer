// Package config owns Steer's harness-neutral operator settings.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/steer-sh/steer-cli/internal/steerpath"
)

// Config is the JSON document stored at ~/.steer/config.json.
type Config struct {
	Reviewer Reviewer `json:"reviewer"`
}

// Reviewer configures the independent CLI that red-teams commit diffs. Timeout
// is measured in seconds, matching the commit gate's environment and hook settings.
type Reviewer struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	Runs     int    `json:"runs,omitempty"`
	Timeout  int    `json:"timeout,omitempty"`
}

// Load reads the global settings. A missing file contributes no overrides and is
// therefore equivalent to the shipped defaults.
func Load() (Config, error) {
	path := steerpath.ConfigFile()
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read steer config %s: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse steer config %s: %w", path, err)
	}
	return cfg, nil
}

// Save atomically replaces the global settings file.
func Save(cfg Config) error {
	path := steerpath.ConfigFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create steer config directory: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode steer config: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.json.*")
	if err != nil {
		return fmt.Errorf("stage steer config: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return fmt.Errorf("set staged steer config permissions: %w", err)
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return fmt.Errorf("write staged steer config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close staged steer config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace steer config: %w", err)
	}
	return nil
}
