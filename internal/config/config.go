// Package config resolves uc's directories and reads settings.json.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Settings mirrors settings.json (architecture §8.5). Fields are pointers
// where "unset" must be distinguishable from the zero value, so partial
// user files merge cleanly onto defaults.
type Settings struct {
	Editor      *string            `json:"editor"`
	HistorySize *int               `json:"history_size"`
	Completion  CompletionSettings `json:"completion"`
	Frecency    FrecencySettings   `json:"frecency"`
}

type CompletionSettings struct {
	Enabled *bool `json:"enabled"`
}

type FrecencySettings struct {
	RecencyWeights RecencyWeights `json:"recency_weights"`
}

type RecencyWeights struct {
	Hour  *float64 `json:"hour"`
	Day   *float64 `json:"day"`
	Week  *float64 `json:"week"`
	Older *float64 `json:"older"`
}

func defaultSettings() Settings {
	return Settings{
		Editor:      nil,
		HistorySize: new(25),
		Completion:  CompletionSettings{Enabled: new(true)},
		Frecency: FrecencySettings{
			RecencyWeights: RecencyWeights{
				Hour:  new(4.0),
				Day:   new(3.0),
				Week:  new(2.0),
				Older: new(1.0),
			},
		},
	}
}

// Config holds resolved directories and loaded settings.
type Config struct {
	UCHome     string // $UC_HOME, default ~/.uc
	ScriptsDir string // $UC_HOME/scripts
	ConfigDir  string // $XDG_CONFIG_HOME/uc, default ~/.config/uc
	Settings   Settings
}

// Load resolves all directories and reads settings.json if present.
// It never writes settings.json itself — a missing file just means
// in-memory defaults are used (architecture §6, decision 13).
func Load() (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}

	ucHome := os.Getenv("UC_HOME")
	if ucHome == "" {
		ucHome = filepath.Join(home, ".uc")
	}

	xdgConfig := os.Getenv("XDG_CONFIG_HOME")
	if xdgConfig == "" {
		xdgConfig = filepath.Join(home, ".config")
	}
	configDir := filepath.Join(xdgConfig, "uc")

	cfg := &Config{
		UCHome:     ucHome,
		ScriptsDir: filepath.Join(ucHome, "scripts"),
		ConfigDir:  configDir,
		Settings:   defaultSettings(),
	}

	settingsPath := filepath.Join(configDir, "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("read %s: %w", settingsPath, err)
	}

	// Unmarshal onto the defaults so unset keys keep their default value
	// (forward-compatible: unknown keys are ignored by encoding/json).
	if err := json.Unmarshal(data, &cfg.Settings); err != nil {
		return nil, fmt.Errorf("parse %s: %w", settingsPath, err)
	}

	return cfg, nil
}

// EnsureDirs creates $UC_HOME and $UC_HOME/scripts if they don't exist.
func (c *Config) EnsureDirs() error {
	if err := os.MkdirAll(c.ScriptsDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", c.ScriptsDir, err)
	}
	return nil
}

// HistoryPath returns the path to history.json.
func (c *Config) HistoryPath() string {
	return filepath.Join(c.UCHome, "history.json")
}

// AliasesPath returns the path to aliases.json (architecture §3.2b).
func (c *Config) AliasesPath() string {
	return filepath.Join(c.ConfigDir, "aliases.json")
}

// FunctionsPath returns the path to functions.json (architecture §3.2c).
func (c *Config) FunctionsPath() string {
	return filepath.Join(c.ConfigDir, "functions.json")
}

// HistorySize returns the configured history cap.
func (c *Config) HistorySize() int {
	if c.Settings.HistorySize != nil {
		return *c.Settings.HistorySize
	}
	return 25
}

// EditorCommand returns the editor to use for `uc edit`: settings.json's
// editor, else $EDITOR, else "vi".
func (c *Config) EditorCommand() string {
	if c.Settings.Editor != nil && *c.Settings.Editor != "" {
		return *c.Settings.Editor
	}
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	return "vi"
}

// CompletionEnabled reports whether ranked __complete callbacks are enabled.
func (c *Config) CompletionEnabled() bool {
	if c.Settings.Completion.Enabled != nil {
		return *c.Settings.Completion.Enabled
	}
	return true
}

// RecencyWeights returns the frecency bucket weights, falling back to
// defaults for any unset field.
func (c *Config) RecencyWeights() (hour, day, week, older float64) {
	d := defaultSettings().Frecency.RecencyWeights
	w := c.Settings.Frecency.RecencyWeights
	hour, day, week, older = *d.Hour, *d.Day, *d.Week, *d.Older
	if w.Hour != nil {
		hour = *w.Hour
	}
	if w.Day != nil {
		day = *w.Day
	}
	if w.Week != nil {
		week = *w.Week
	}
	if w.Older != nil {
		older = *w.Older
	}
	return
}
