package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

// fakeEditor points $EDITOR at a shell stub whose body runs with "$1" set
// to the file being edited, so New's post-edit branches can be driven from
// a unit test.
func fakeEditor(t *testing.T, body string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("editor stub needs a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "fake-editor")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", path)
}

func TestNewCreatesAndRegistersScript(t *testing.T) {
	r, out := newTestRunner(t)
	fakeEditor(t, `printf 'echo hi\n' >> "$1"`)

	if err := r.New("t1", "sh"); err != nil {
		t.Fatalf("New: %v", err)
	}
	if !strings.Contains(out.String(), "created") {
		t.Errorf("output = %q, want created-line", out.String())
	}

	info, err := os.Stat(filepath.Join(r.Cfg.ScriptsDir, "t1.sh"))
	if err != nil {
		t.Fatalf("script missing: %v", err)
	}
	if info.Mode()&0o100 == 0 {
		t.Errorf("script not executable: mode %v", info.Mode())
	}

	resolved, err := r.Reg.Resolve("t1")
	if err != nil {
		t.Fatalf("Resolve after New: %v", err)
	}
	if resolved.Kind != registry.KindScript {
		t.Errorf("resolved kind = %q, want script", resolved.Kind)
	}
	// The scaffolded metadata block must be shaped so ExtractMeta reads it
	// back — that's the whole point of embedding it.
	if resolved.Usage != "t1 <args>" {
		t.Errorf("usage = %q, want %q", resolved.Usage, "t1 <args>")
	}
}

// An untouched template means "I changed my mind": nothing is registered,
// and it's not an error.
func TestNewAbortsOnUnchangedTemplate(t *testing.T) {
	r, out := newTestRunner(t)
	fakeEditor(t, "exit 0")

	if err := r.New("t1", "sh"); err != nil {
		t.Fatalf("New: %v", err)
	}
	if !strings.Contains(out.String(), "aborted") {
		t.Errorf("output = %q, want aborted-line", out.String())
	}
	if _, err := os.Stat(filepath.Join(r.Cfg.ScriptsDir, "t1.sh")); !os.IsNotExist(err) {
		t.Errorf("template file survived an abort: stat err = %v", err)
	}
	if _, err := r.Reg.Resolve("t1"); err == nil {
		t.Error("Resolve succeeded after an abort, want not-found")
	}
}

func TestNewRemovesScaffoldWhenEditorFails(t *testing.T) {
	r, _ := newTestRunner(t)
	fakeEditor(t, `printf 'echo hi\n' >> "$1"; exit 1`)

	err := r.New("t1", "sh")
	if err == nil {
		t.Fatal("New: want error from a failing editor, got nil")
	}
	if !strings.Contains(err.Error(), "nothing registered") {
		t.Errorf("error = %q, want nothing-registered", err)
	}
	if _, err := os.Stat(filepath.Join(r.Cfg.ScriptsDir, "t1.sh")); !os.IsNotExist(err) {
		t.Errorf("scaffold survived an editor failure: stat err = %v", err)
	}
}

func TestNewRejectsBadNames(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"list", "reserved subcommand name"},
		{"new", "reserved subcommand name"},
		{"../evil", "invalid script name"},
		{"a/b", "invalid script name"},
	}
	for _, c := range cases {
		r, _ := newTestRunner(t)
		// $EDITOR must never be reached, so leave it as a command that
		// would fail loudly if it were.
		t.Setenv("EDITOR", "/nonexistent/editor")
		err := r.New(c.name, "sh")
		if err == nil {
			t.Errorf("New(%q): want error, got nil", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("New(%q) error = %q, want %q", c.name, err, c.want)
		}
	}
}

// A same-named script under any extension already resolves, so scaffolding
// over it would create an ambiguity instead of a new command.
func TestNewRejectsExistingName(t *testing.T) {
	r, _ := newTestRunner(t)
	if err := r.Cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	writeScript(t, r.Cfg.ScriptsDir, "t1.py", "#!/usr/bin/env python3\nprint('hi')\n")
	t.Setenv("EDITOR", "/nonexistent/editor")

	err := r.New("t1", "sh")
	if err == nil {
		t.Fatal("New over an existing name: want error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want already-exists", err)
	}
	if _, err := os.Stat(filepath.Join(r.Cfg.ScriptsDir, "t1.sh")); !os.IsNotExist(err) {
		t.Errorf("t1.sh was created anyway: stat err = %v", err)
	}
}

// An already-ambiguous bare name can't be resolved to report "already
// exists", so New surfaces the ambiguity itself rather than adding a third
// file to the pile.
func TestNewRejectsAmbiguousName(t *testing.T) {
	r, _ := newTestRunner(t)
	if err := r.Cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	writeScript(t, r.Cfg.ScriptsDir, "t1.py", "#!/usr/bin/env python3\nprint('hi')\n")
	writeScript(t, r.Cfg.ScriptsDir, "t1.rb", "#!/usr/bin/env ruby\nputs 'hi'\n")
	t.Setenv("EDITOR", "/nonexistent/editor")

	err := r.New("t1", "sh")
	if err == nil {
		t.Fatal("New over an ambiguous name: want error, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error = %q, want ambiguous-name", err)
	}
	if _, err := os.Stat(filepath.Join(r.Cfg.ScriptsDir, "t1.sh")); !os.IsNotExist(err) {
		t.Errorf("t1.sh was created anyway: stat err = %v", err)
	}
}

func TestNewRejectsUnknownLang(t *testing.T) {
	r, _ := newTestRunner(t)
	t.Setenv("EDITOR", "/nonexistent/editor")

	err := r.New("t1", "go")
	if err == nil {
		t.Fatal("New with an unknown lang: want error, got nil")
	}
	for _, want := range []string{`unknown --lang "go"`, "sh|py|js|rb|pl"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %q", err, want)
		}
	}
}

func TestNewLangSetsExtensionAndShebang(t *testing.T) {
	r, _ := newTestRunner(t)
	fakeEditor(t, `printf 'print("hi")\n' >> "$1"`)

	if err := r.New("t1", "py"); err != nil {
		t.Fatalf("New: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(r.Cfg.ScriptsDir, "t1.py"))
	if err != nil {
		t.Fatalf("t1.py missing: %v", err)
	}
	if !strings.HasPrefix(string(body), "#!/usr/bin/env python3\n") {
		t.Errorf("t1.py = %q, want a python3 shebang", body)
	}
}

// `uc new t1.sh` must not produce t1.sh.sh.
func TestNewDoesNotDoubleExtension(t *testing.T) {
	r, _ := newTestRunner(t)
	fakeEditor(t, `printf 'echo hi\n' >> "$1"`)

	if err := r.New("t1.sh", "sh"); err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.Cfg.ScriptsDir, "t1.sh")); err != nil {
		t.Errorf("t1.sh missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.Cfg.ScriptsDir, "t1.sh.sh")); !os.IsNotExist(err) {
		t.Errorf("t1.sh.sh was created: stat err = %v", err)
	}
}

// A hazardous alias command is still registered — the note is advisory and
// belongs on stderr, so stdout stays clean for scripting.
func TestAliasAddHazardWarnsWithoutFailing(t *testing.T) {
	r, out := newTestRunner(t)

	if err := r.AliasAdd("noted", "echo hi # my note", ""); err != nil {
		t.Fatalf("AliasAdd: %v", err)
	}
	if strings.Contains(out.String(), "note:") {
		t.Errorf("stdout = %q, want the hazard note on stderr only", out.String())
	}

	resolved, err := r.Reg.Resolve("noted")
	if err != nil {
		t.Fatalf("Resolve after AliasAdd: %v", err)
	}
	if resolved.Kind != registry.KindAlias {
		t.Errorf("resolved kind = %q, want alias", resolved.Kind)
	}
}

func TestCompleteDescribeEmitsTabSeparatedDesc(t *testing.T) {
	r, out := newTestRunner(t)
	if err := os.MkdirAll(r.Cfg.ScriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, r.Cfg.ScriptsDir, "killport", "#!/bin/sh\n# @desc: kills a port\necho hi\n")

	if err := r.Complete("k", true); err != nil {
		t.Fatalf("Complete describe: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "killport\tkills a port\n") {
		t.Errorf("output = %q, want a tab-separated description", got)
	}
}

// In describe mode a candidate with no description must emit a bare name, not
// a trailing tab — fish would otherwise render an empty pager description.
func TestCompleteDescribeOmitsTabWhenNoDesc(t *testing.T) {
	r, out := newTestRunner(t)
	if err := os.MkdirAll(r.Cfg.ScriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, r.Cfg.ScriptsDir, "kbare", "#!/bin/sh\necho hi\n")

	if err := r.Complete("kbare", true); err != nil {
		t.Fatalf("Complete describe: %v", err)
	}
	if got := out.String(); got != "kbare\n" {
		t.Errorf("output = %q, want %q", got, "kbare\n")
	}
}

// Describing must not change which candidates appear, only how they print.
func TestCompleteDescribeMatchesPlainCandidateSet(t *testing.T) {
	r, out := newTestRunner(t)
	if err := os.MkdirAll(r.Cfg.ScriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, r.Cfg.ScriptsDir, "kbare", "#!/bin/sh\necho hi\n")
	writeScript(t, r.Cfg.ScriptsDir, "killport", "#!/bin/sh\n# @desc: kills a port\necho hi\n")

	if err := r.Complete("k", false); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	plain := strings.Split(strings.TrimSpace(out.String()), "\n")

	out.Reset()
	if err := r.Complete("k", true); err != nil {
		t.Fatalf("Complete describe: %v", err)
	}
	var described []string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		name, _, _ := strings.Cut(line, "\t")
		described = append(described, name)
	}

	if !slices.Equal(plain, described) {
		t.Errorf("described names = %v, want %v", described, plain)
	}
}

// completion.enabled false silences both forms — the shell still calls back
// in, but gets nothing.
func TestCompleteDisabledPrintsNothing(t *testing.T) {
	r, out := newTestRunner(t)
	if err := os.MkdirAll(r.Cfg.ScriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, r.Cfg.ScriptsDir, "killport", "#!/bin/sh\n# @desc: kills a port\necho hi\n")
	disabled := false
	r.Cfg.Settings.Completion.Enabled = &disabled

	for _, describe := range []bool{false, true} {
		out.Reset()
		if err := r.Complete("k", describe); err != nil {
			t.Fatalf("Complete(describe=%v): %v", describe, err)
		}
		if out.String() != "" {
			t.Errorf("Complete(describe=%v) printed %q, want nothing", describe, out.String())
		}
	}
}

func TestCompleteWithoutDescribeEmitsBareName(t *testing.T) {
	r, out := newTestRunner(t)
	if err := os.MkdirAll(r.Cfg.ScriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript(t, r.Cfg.ScriptsDir, "killport", "#!/bin/sh\n# @desc: kills a port\necho hi\n")

	if err := r.Complete("k", false); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := out.String(); got != "killport\n" {
		t.Errorf("output = %q, want %q", got, "killport\n")
	}
}
