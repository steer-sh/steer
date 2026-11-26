// Package config owns Steer's harness-neutral operator settings.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/steer-sh/steer-cli/internal/atomicfile"
	"github.com/steer-sh/steer-cli/internal/steerpath"
)

// Config is the JSON document stored at ~/.steer/config.json.
type Config struct {
	Reviewer Reviewer `json:"reviewer"`
	// BranchReviewer configures `steer review branch`; settings.BranchConfig says
	// which unset keys inherit from Reviewer.
	BranchReviewer BranchReviewer `json:"branch_reviewer,omitzero"`
	Protection     Protection     `json:"protection,omitzero"`
}

// BranchReviewer configures the reviewer of a whole branch before a pull request.
// Timeout is in seconds.
type BranchReviewer struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	Runs     int    `json:"runs,omitempty"`
	Timeout  int    `json:"timeout,omitempty"`
}

// Protection configures the guard that keeps an agent from changing Steer's own
// state. Self is "on", "off", or empty for the default, which is on.
type Protection struct {
	Self string `json:"self,omitempty"`
}

// Reviewer configures the independent CLI that red-teams commit diffs. Timeout
// is measured in seconds, matching the commit gate's environment and hook settings.
type Reviewer struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	Runs     int    `json:"runs,omitempty"`
	Timeout  int    `json:"timeout,omitempty"`
	// Rounds is how many reviews in one sequence may block a commit.
	Rounds int `json:"rounds,omitempty"`
	API    API `json:"api,omitzero"`
}

// API configures the openai-compatible provider. It holds no key: Save writes this
// file readable by every user, so the key lives in its own file (internal/apikey) or
// in the environment variable KeyEnv names.
type API struct {
	BaseURL string `json:"base_url,omitempty"`
	KeyEnv  string `json:"key_env,omitempty"`
	Tools   string `json:"tools,omitempty"`
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
	if err := atomicfile.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("replace steer config: %w", err)
	}
	return nil
}
