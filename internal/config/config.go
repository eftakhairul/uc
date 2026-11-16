// Package config resolves uc's directories and reads settings.json.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/eftakhairul/uc/internal/atomicfile"
	"github.com/eftakhairul/uc/internal/jsonerr"
	"github.com/eftakhairul/uc/internal/store"
)

// DirMode is the mode for directories uc creates, and ScriptMode for the
// scripts it installs: both private to the user, since scripts often hold
// tokens and internal hostnames.
const (
	DirMode    = atomicfile.DirMode
	ScriptMode = 0o700
)

// MaxHistorySize is the largest history_size accepted from settings.json.
const MaxHistorySize = 10000

// defaultHistorySize is used when history_size is unset or out of range.
const defaultHistorySize = 25

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
		HistorySize: new(defaultHistorySize),
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

	// Warnings are problems found in settings.json while loading. They
	// never stop a command; main prints them.
	Warnings []string

	store *store.Store
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
		return nil, fmt.Errorf("cannot read %s: %w", settingsPath, err)
	}

	// Unmarshal onto the defaults so unset keys keep their default value
	// (forward-compatible: unknown keys are ignored by encoding/json). A
	// broken file must not lock the user out of uc — not even of `uc help`
	// — so it is reported and ignored rather than returned as an error.
	if err := json.Unmarshal(data, &cfg.Settings); err != nil {
		cfg.Settings = defaultSettings()
		cfg.warnf("ignoring settings.json: %s — using defaults", jsonerr.Describe(data, err))
		return cfg, nil
	}
	cfg.validate()
	return cfg, nil
}

func (c *Config) warnf(format string, a ...any) {
	c.Warnings = append(c.Warnings, fmt.Sprintf(format, a...))
}

// validate resets out-of-range settings to their defaults, one warning
// each, leaving the rest of the file in effect.
func (c *Config) validate() {
	if h := c.Settings.HistorySize; h != nil && (*h < 0 || *h > MaxHistorySize) {
		c.warnf(`ignoring "history_size" in settings.json: must be between 0 and %d — using %d`, MaxHistorySize, defaultHistorySize)
		c.Settings.HistorySize = new(defaultHistorySize)
	}
	d := defaultSettings().Frecency.RecencyWeights
	w := &c.Settings.Frecency.RecencyWeights
	for _, f := range []struct {
		key string
		val **float64
		def *float64
	}{
		{"hour", &w.Hour, d.Hour},
		{"day", &w.Day, d.Day},
		{"week", &w.Week, d.Week},
		{"older", &w.Older, d.Older},
	} {
		if *f.val != nil && **f.val < 0 {
			c.warnf(`ignoring "frecency.recency_weights.%s" in settings.json: must not be negative — using %g`, f.key, *f.def)
			*f.val = f.def
		}
	}
}

// EnsureDirs creates $UC_HOME and $UC_HOME/scripts if they don't exist.
func (c *Config) EnsureDirs() error {
	if err := os.MkdirAll(c.ScriptsDir, DirMode); err != nil {
		return fmt.Errorf("create %s: %w", c.ScriptsDir, err)
	}
	return nil
}

// HistoryPath returns the path to history.json.
func (c *Config) HistoryPath() string {
	return filepath.Join(c.UCHome, "history.json")
}

// CommandsPath returns the path to commands.json, which holds every alias
// and function.
func (c *Config) CommandsPath() string {
	return filepath.Join(c.ConfigDir, "commands.json")
}

// AliasesPath returns the path to the pre-merge aliases.json, read only to
// migrate it into commands.json.
func (c *Config) AliasesPath() string {
	return filepath.Join(c.ConfigDir, "aliases.json")
}

// FunctionsPath returns the path to the pre-merge functions.json, read only
// to migrate it into commands.json.
func (c *Config) FunctionsPath() string {
	return filepath.Join(c.ConfigDir, "functions.json")
}

// Store returns the alias/function store, created on first use and shared
// for the life of the Config so its once-per-process warnings stay
// once-per-process.
func (c *Config) Store() *store.Store {
	if c.store == nil {
		c.store = store.New(c.CommandsPath(), c.AliasesPath(), c.FunctionsPath())
	}
	return c.store
}

// SyncDir returns the path to the `uc sync` staging repo.
func (c *Config) SyncDir() string {
	return filepath.Join(c.UCHome, "sync")
}

// HistorySize returns the configured history cap. 0 means history is off.
func (c *Config) HistorySize() int {
	if c.Settings.HistorySize != nil {
		return *c.Settings.HistorySize
	}
	return defaultHistorySize
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
