package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir reads USERPROFILE on Windows
	t.Setenv("UC_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	// The vi fallback below is only reachable with $EDITOR unset; without
	// this the test passes or fails depending on the developer's shell.
	t.Setenv("EDITOR", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.UCHome != filepath.Join(home, ".uc") {
		t.Errorf("UCHome = %q", cfg.UCHome)
	}
	if cfg.HistorySize() != 25 {
		t.Errorf("HistorySize = %d, want 25", cfg.HistorySize())
	}
	if !cfg.CompletionEnabled() {
		t.Errorf("CompletionEnabled = false, want true")
	}
	if cfg.EditorCommand() != "vi" {
		t.Errorf("EditorCommand = %q, want vi (no $EDITOR set)", cfg.EditorCommand())
	}
}

func TestLoad_UCHomeOverride(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	custom := filepath.Join(home, "custom-uc")
	t.Setenv("UC_HOME", custom)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.UCHome != custom {
		t.Errorf("UCHome = %q, want %q", cfg.UCHome, custom)
	}
	if cfg.ScriptsDir != filepath.Join(custom, "scripts") {
		t.Errorf("ScriptsDir = %q", cfg.ScriptsDir)
	}
}

func TestLoad_PartialSettingsFileMergesOntoDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("UC_HOME", "")
	xdg := filepath.Join(home, "xdgconfig")
	t.Setenv("XDG_CONFIG_HOME", xdg)

	configDir := filepath.Join(xdg, "uc")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{"history_size": 10, "unknown_future_key": "ignored"}`
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HistorySize() != 10 {
		t.Errorf("HistorySize = %d, want 10", cfg.HistorySize())
	}
	// completion.enabled wasn't in the file; default should still apply.
	if !cfg.CompletionEnabled() {
		t.Errorf("CompletionEnabled = false, want true (default preserved)")
	}
}

func TestLoad_NoSettingsFileDoesNotCreateOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("UC_HOME", "")
	xdg := filepath.Join(home, "xdgconfig")
	t.Setenv("XDG_CONFIG_HOME", xdg)

	if _, err := Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := os.Stat(filepath.Join(xdg, "uc", "settings.json")); !os.IsNotExist(err) {
		t.Errorf("expected settings.json to not exist, stat err = %v", err)
	}
}

// writeSettings points HOME/XDG at a temp dir holding settings.json with
// the given content.
func writeSettings(t *testing.T, content string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("UC_HOME", "")
	xdg := filepath.Join(home, "xdgconfig")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := os.MkdirAll(filepath.Join(xdg, "uc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(xdg, "uc", "settings.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadBrokenSettingsWarns(t *testing.T) {
	for name, c := range map[string]struct {
		content string
		warning string
	}{
		"syntax": {"{\n  \"history_size\": 10,\n  oops\n}", "ignoring settings.json: invalid JSON at line 3 — using defaults"},
		"type":   {`{"history_size": "x"}`, `ignoring settings.json: "history_size" must be a whole number — using defaults`},
		"nested": {`{"frecency": {"recency_weights": {"hour": true}}}`, `ignoring settings.json: "frecency.recency_weights.hour" must be a number — using defaults`},
	} {
		t.Run(name, func(t *testing.T) {
			writeSettings(t, c.content)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v — a broken settings.json must not be fatal", err)
			}
			if len(cfg.Warnings) != 1 || cfg.Warnings[0] != c.warning {
				t.Errorf("Warnings = %q, want [%q]", cfg.Warnings, c.warning)
			}
			if cfg.HistorySize() != 25 {
				t.Errorf("HistorySize = %d, want the default 25", cfg.HistorySize())
			}
		})
	}
}

func TestLoadOutOfRangeSettingsWarn(t *testing.T) {
	writeSettings(t, `{"history_size": -3, "editor": "nano", "frecency": {"recency_weights": {"day": -1, "week": 7}}}`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Warnings) != 2 {
		t.Fatalf("Warnings = %q, want 2", cfg.Warnings)
	}
	if cfg.HistorySize() != 25 {
		t.Errorf("HistorySize = %d, want 25", cfg.HistorySize())
	}
	if _, day, week, _ := cfg.RecencyWeights(); day != 3 || week != 7 {
		t.Errorf("day, week = %g, %g; want the default 3 and the valid 7", day, week)
	}
	if cfg.EditorCommand() != "nano" {
		t.Errorf("valid keys must survive: editor = %q", cfg.EditorCommand())
	}

	writeSettings(t, `{"history_size": 10001}`)
	if cfg, _ = Load(); len(cfg.Warnings) != 1 || cfg.HistorySize() != 25 {
		t.Errorf("history_size 10001: Warnings = %q, size %d", cfg.Warnings, cfg.HistorySize())
	}
}

func TestLoadHistorySizeZeroIsValid(t *testing.T) {
	writeSettings(t, `{"history_size": 0}`)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Warnings) != 0 || cfg.HistorySize() != 0 {
		t.Errorf("Warnings = %q, HistorySize = %d; want none and 0 (history off)", cfg.Warnings, cfg.HistorySize())
	}
}

func TestStoreIsSharedAndUsesCommandsJSON(t *testing.T) {
	cfg := &Config{ConfigDir: t.TempDir()}
	if cfg.Store() != cfg.Store() {
		t.Error("Store() returned different instances")
	}
	if cfg.Store().Path != filepath.Join(cfg.ConfigDir, "commands.json") {
		t.Errorf("Store().Path = %q", cfg.Store().Path)
	}
	if cfg.Store().LegacyAliases != cfg.AliasesPath() || cfg.Store().LegacyFunctions != cfg.FunctionsPath() {
		t.Error("legacy paths not wired")
	}
}
