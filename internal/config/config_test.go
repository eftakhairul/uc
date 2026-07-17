package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_Defaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("UC_HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")

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
