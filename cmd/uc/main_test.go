package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eftakhairul/uc/internal/commands"
	"github.com/eftakhairul/uc/internal/config"
)

// newTestRunner builds a Runner over temp directories with output captured
// in a buffer.
func newTestRunner(t *testing.T) (*commands.Runner, *bytes.Buffer) {
	t.Helper()
	ucHome := filepath.Join(t.TempDir(), "uc-home")
	r := commands.New(&config.Config{
		UCHome:     ucHome,
		ScriptsDir: filepath.Join(ucHome, "scripts"),
		ConfigDir:  filepath.Join(t.TempDir(), "config"),
	})
	out := &bytes.Buffer{}
	r.Out = out
	return r, out
}

func TestRunAddUsage(t *testing.T) {
	r, _ := newTestRunner(t)
	for _, args := range [][]string{
		nil,
		{"--force"},
		{"a", "b", "c"},
		{"https://example.com/x.sh", "x", "extra", "--force"},
	} {
		err := runAdd(r, args)
		if err == nil || err.Error() != "usage: uc add <path|url> [name] [--force]" {
			t.Errorf("runAdd(%q) error = %v, want usage", args, err)
		}
	}
}

func TestRunAddURLWithForceAnywhere(t *testing.T) {
	// The served script echoes the ?v= query, so each add is identifiable
	// without sharing state with the handler goroutine.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, "#!/bin/sh\necho %s\n", req.URL.Query().Get("v"))
	}))
	t.Cleanup(srv.Close)
	r, out := newTestRunner(t)
	url := srv.URL + "/tool.sh"

	if err := runAdd(r, []string{url, "tool"}); err != nil {
		t.Fatalf("runAdd(url, name): %v", err)
	}
	if !strings.Contains(out.String(), "review it before first run") {
		t.Errorf("output = %q, want review note", out.String())
	}

	// --force is recognised before, between, or after the positionals.
	for i, args := range [][]string{
		{"--force", url + "?v=pos0", "tool"},
		{url + "?v=pos1", "--force", "tool"},
		{url + "?v=pos2", "tool", "--force"},
	} {
		version := fmt.Sprintf("pos%d", i)
		if err := runAdd(r, args); err != nil {
			t.Fatalf("runAdd(%q): %v", args, err)
		}
		data, err := os.ReadFile(filepath.Join(r.Cfg.ScriptsDir, "tool"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), version) {
			t.Errorf("runAdd(%q) did not overwrite: %q", args, data)
		}
	}
}
