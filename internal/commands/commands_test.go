package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/eftakhairul/uc/internal/config"
	"github.com/eftakhairul/uc/internal/registry"
)

// newTestRunner builds a Runner over temp directories with output captured
// in a buffer.
func newTestRunner(t *testing.T) (*Runner, *bytes.Buffer) {
	t.Helper()
	ucHome := filepath.Join(t.TempDir(), "uc-home")
	cfg := &config.Config{
		UCHome:     ucHome,
		ScriptsDir: filepath.Join(ucHome, "scripts"),
		ConfigDir:  filepath.Join(t.TempDir(), "config"),
	}
	out := &bytes.Buffer{}
	reg := registry.New(cfg.ScriptsDir, cfg.AliasesPath(), cfg.FunctionsPath())
	return &Runner{Cfg: cfg, Reg: reg, Out: out}, out
}

// writeScript creates a source script outside the registry, ready for Add.
func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAddRejectsTraversalNames(t *testing.T) {
	r, _ := newTestRunner(t)
	src := writeScript(t, t.TempDir(), "x.sh", "#!/bin/sh\necho hi\n")

	for _, name := range []string{"../evil", "a/b", `a\b`, ".", ".."} {
		err := r.Add(src, name, false)
		if err == nil {
			t.Errorf("Add(name=%q): want error, got nil", name)
			continue
		}
		if !strings.Contains(err.Error(), "invalid script name") {
			t.Errorf("Add(name=%q) error = %q, want invalid-script-name", name, err)
		}
	}

	// Nothing may have escaped ScriptsDir — its parent must hold nothing
	// beyond the (possibly created) scripts dir itself.
	entries, err := os.ReadDir(r.Cfg.UCHome)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "scripts" {
			t.Errorf("unexpected file escaped into UCHome: %s", e.Name())
		}
	}
}

func TestAddRejectsDuplicate(t *testing.T) {
	r, _ := newTestRunner(t)
	dir := t.TempDir()
	src1 := writeScript(t, dir, "one.sh", "#!/bin/sh\necho one\n")
	src2 := writeScript(t, dir, "two.sh", "#!/bin/sh\necho two\n")

	if err := r.Add(src1, "dup", false); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	err := r.Add(src2, "dup", false)
	if err == nil {
		t.Fatal("duplicate Add: want error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("duplicate Add error = %q, want already-exists", err)
	}

	// The original content must be untouched.
	data, readErr := os.ReadFile(filepath.Join(r.Cfg.ScriptsDir, "dup"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(data), "echo one") {
		t.Errorf("original script was modified: %q", data)
	}
}

func TestAddForceOverwrites(t *testing.T) {
	r, _ := newTestRunner(t)
	dir := t.TempDir()
	src1 := writeScript(t, dir, "one.sh", "#!/bin/sh\necho one\n")
	src2 := writeScript(t, dir, "two.sh", "#!/bin/sh\necho two\n")

	if err := r.Add(src1, "dup", false); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	if err := r.Add(src2, "dup", true); err != nil {
		t.Fatalf("force Add: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(r.Cfg.ScriptsDir, "dup"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "echo two") {
		t.Errorf("force Add did not replace content: %q", data)
	}
}

func TestAddRejectsCrossExtensionAmbiguity(t *testing.T) {
	r, _ := newTestRunner(t)
	dir := t.TempDir()
	srcSh := writeScript(t, dir, "tool.sh", "#!/bin/sh\necho sh\n")
	srcPy := writeScript(t, dir, "tool.py", "print('py')\n")

	if err := r.Add(srcSh, "", false); err != nil {
		t.Fatalf("Add tool.sh: %v", err)
	}
	err := r.Add(srcPy, "", false)
	if err == nil {
		t.Fatal("Add tool.py beside tool.sh: want ambiguity error, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("Add error = %q, want ambiguous", err)
	}

	if err := r.Add(srcPy, "", true); err != nil {
		t.Fatalf("force Add tool.py: %v", err)
	}
}

func TestAddDerivedNameAndOutput(t *testing.T) {
	r, out := newTestRunner(t)
	src := writeScript(t, t.TempDir(), "killport.sh", "#!/bin/sh\n# @desc: kills a port\necho ok\n")

	if err := r.Add(src, "", false); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if !strings.Contains(out.String(), "added") {
		t.Errorf("output = %q, want added-line", out.String())
	}

	info, err := os.Stat(filepath.Join(r.Cfg.ScriptsDir, "killport.sh"))
	if err != nil {
		t.Fatalf("dest script missing: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode()&0o100 == 0 {
		t.Errorf("dest script not executable: mode %v", info.Mode())
	}

	resolved, err := r.Reg.Resolve("killport")
	if err != nil {
		t.Fatalf("Resolve after Add: %v", err)
	}
	if resolved.Kind != registry.KindScript {
		t.Errorf("resolved kind = %q, want script", resolved.Kind)
	}
}
