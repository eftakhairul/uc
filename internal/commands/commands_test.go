package commands

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// scriptServer serves routes (URL path -> body) and counts every request,
// so tests can assert that guard failures never reach the network.
// Unknown paths get a 404.
func scriptServer(t *testing.T, routes map[string]string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	hits := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		hits.Add(1)
		body, ok := routes[req.URL.Path]
		if !ok {
			http.NotFound(w, req)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, hits
}

// assertNoScripts fails if ScriptsDir holds any file.
func assertNoScripts(t *testing.T, r *Runner) {
	t.Helper()
	entries, err := os.ReadDir(r.Cfg.ScriptsDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("unexpected script written: %s", e.Name())
	}
}

func TestIsURL(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"https://example.com/x.sh", true},
		{"http://example.com/x.sh", true},
		{"HTTPS://example.com/x.sh", true},
		{"./x.sh", false},
		{"/tmp/https://x.sh", false},
		{"ftp://example.com/x.sh", false},
		{"https:x.sh", false},
		{"", false},
	} {
		if got := isURL(tc.in); got != tc.want {
			t.Errorf("isURL(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestRawURL(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		// Gist page URLs get /raw appended.
		{"https://gist.github.com/user/abc123", "https://gist.github.com/user/abc123/raw"},
		{"https://gist.github.com/user/abc123/", "https://gist.github.com/user/abc123/raw"},
		{"https://GIST.github.com/user/abc123", "https://GIST.github.com/user/abc123/raw"},
		{"https://gist.github.com:443/user/abc123", "https://gist.github.com:443/user/abc123/raw"},
		{"https://gist.github.com/user/abc123?file=a", "https://gist.github.com/user/abc123/raw?file=a"},
		// Gist URLs already pointing at raw content are unchanged.
		{"https://gist.github.com/user/abc123/raw", "https://gist.github.com/user/abc123/raw"},
		{"https://gist.github.com/user/abc123/raw/def/killport.sh", "https://gist.github.com/user/abc123/raw/def/killport.sh"},
		// Non-gist URLs pass through.
		{"https://raw.githubusercontent.com/u/r/main/killport.sh", "https://raw.githubusercontent.com/u/r/main/killport.sh"},
		{"http://example.com/tool.py?x=1#frag", "http://example.com/tool.py?x=1#frag"},
		{"https://gist.githubusercontent.com/user/abc123/raw/killport.sh", "https://gist.githubusercontent.com/user/abc123/raw/killport.sh"},
		{"https://notgist.github.com/user/abc123", "https://notgist.github.com/user/abc123"},
	} {
		got, err := rawURL(tc.in)
		if err != nil {
			t.Errorf("rawURL(%q): unexpected error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("rawURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRawURLRejectsInvalid(t *testing.T) {
	for _, in := range []string{
		"https://",           // no host
		"http://%zz/x.sh",    // unparsable host escape
		"ftp://example.com/", // unsupported scheme
		"example.com/x.sh",   // no scheme
		"https://exa mple.com/x.sh",
	} {
		if got, err := rawURL(in); err == nil {
			t.Errorf("rawURL(%q) = %q, want error", in, got)
		} else if !strings.Contains(err.Error(), "invalid URL") {
			t.Errorf("rawURL(%q) error = %q, want invalid-URL", in, err)
		}
	}
}

func TestNameFromURL(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"https://example.com/killport.sh", "killport.sh"},
		{"https://example.com/a/b/tool", "tool"},
		{"https://example.com/tool.py?token=secret#L10", "tool.py"},
		{"https://example.com/dir/tool.sh/", "tool.sh"},
		{"https://example.com/my%20tool.sh", "my tool.sh"},
		{"https://gist.github.com/user/abc123/raw/def/killport.sh", "killport.sh"},
	} {
		got, err := nameFromURL(tc.in)
		if err != nil {
			t.Errorf("nameFromURL(%q): unexpected error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("nameFromURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	for _, in := range []string{
		"https://example.com",
		"https://example.com/",
		"https://example.com/?x=1",
		"https://gist.github.com/user/abc123/raw",
		"https://example.com/raw/",
	} {
		if got, err := nameFromURL(in); err == nil {
			t.Errorf("nameFromURL(%q) = %q, want error", in, got)
		} else if !strings.Contains(err.Error(), "pass one explicitly") {
			t.Errorf("nameFromURL(%q) error = %q, want pass-one-explicitly", in, err)
		}
	}
}

func TestAddURLInstallsScript(t *testing.T) {
	const body = "#!/bin/sh\n# @desc: kills a port\necho ok\n"
	srv, hits := scriptServer(t, map[string]string{"/scripts/killport.sh": body})
	r, out := newTestRunner(t)

	srcURL := srv.URL + "/scripts/killport.sh"
	if err := r.Add(srcURL, "", false); err != nil {
		t.Fatalf("Add(url): %v", err)
	}
	if hits.Load() != 1 {
		t.Errorf("server hits = %d, want 1", hits.Load())
	}

	dest := filepath.Join(r.Cfg.ScriptsDir, "killport.sh")
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("dest script missing: %v", err)
	}
	if string(data) != body {
		t.Errorf("installed content = %q, want %q", data, body)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Errorf("dest mode = %v, want 0755", info.Mode().Perm())
	}

	got := out.String()
	for _, want := range []string{
		"added " + srcURL + " -> " + dest,
		"review it before first run",
		"uc which killport\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output = %q, want it to contain %q", got, want)
		}
	}

	resolved, err := r.Reg.Resolve("killport")
	if err != nil {
		t.Fatalf("Resolve after Add(url): %v", err)
	}
	if resolved.Kind != registry.KindScript {
		t.Errorf("resolved kind = %q, want script", resolved.Kind)
	}
}

func TestAddURLExplicitNameOverridesDerived(t *testing.T) {
	srv, _ := scriptServer(t, map[string]string{"/raw/abc/tool.sh": "#!/bin/sh\necho hi\n"})
	r, out := newTestRunner(t)

	if err := r.Add(srv.URL+"/raw/abc/tool.sh", "hello", false); err != nil {
		t.Fatalf("Add(url, name): %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.Cfg.ScriptsDir, "hello")); err != nil {
		t.Errorf("explicit name not used: %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.Cfg.ScriptsDir, "tool.sh")); err == nil {
		t.Error("derived name was used despite explicit name")
	}
	if !strings.Contains(out.String(), "uc which hello") {
		t.Errorf("output = %q, want review note naming hello", out.String())
	}
}

func TestAddURLNameIgnoresQueryAndFragment(t *testing.T) {
	srv, _ := scriptServer(t, map[string]string{"/tool.sh": "#!/bin/sh\necho hi\n"})
	r, _ := newTestRunner(t)

	if err := r.Add(srv.URL+"/tool.sh?token=abc#top", "", false); err != nil {
		t.Fatalf("Add(url with query): %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.Cfg.ScriptsDir, "tool.sh")); err != nil {
		t.Errorf("want script named tool.sh: %v", err)
	}
}

func TestAddURLNon200WritesNothing(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError, http.StatusNoContent} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, "#!/bin/sh\necho should-not-install\n")
			}))
			t.Cleanup(srv.Close)
			r, out := newTestRunner(t)

			err := r.Add(srv.URL+"/tool.sh", "", false)
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if !strings.Contains(err.Error(), "fetch") || !strings.Contains(err.Error(), fmt.Sprint(status)) {
				t.Errorf("error = %q, want fetch error carrying status %d", err, status)
			}
			assertNoScripts(t, r)
			if out.Len() != 0 {
				t.Errorf("output = %q, want none on failure", out.String())
			}
		})
	}
}

func TestAddURLFollowsRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/user/abc123/raw":
			http.Redirect(w, req, "/content/killport.sh", http.StatusFound)
		case "/content/killport.sh":
			fmt.Fprint(w, "#!/bin/sh\necho redirected\n")
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(srv.Close)
	r, _ := newTestRunner(t)

	if err := r.Add(srv.URL+"/user/abc123/raw", "killport", false); err != nil {
		t.Fatalf("Add(redirecting url): %v", err)
	}
	data, err := os.ReadFile(filepath.Join(r.Cfg.ScriptsDir, "killport"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "redirected") {
		t.Errorf("content = %q, want redirect target body", data)
	}
}

func TestAddURLUnderivableNameSkipsFetch(t *testing.T) {
	srv, hits := scriptServer(t, map[string]string{"/": "#!/bin/sh\n", "/raw": "#!/bin/sh\n"})
	r, _ := newTestRunner(t)

	for _, u := range []string{srv.URL, srv.URL + "/", srv.URL + "/raw"} {
		err := r.Add(u, "", false)
		if err == nil {
			t.Errorf("Add(%q): want error, got nil", u)
			continue
		}
		if !strings.Contains(err.Error(), "cannot derive a name") {
			t.Errorf("Add(%q) error = %q, want cannot-derive", u, err)
		}
	}
	if hits.Load() != 0 {
		t.Errorf("server hits = %d, want 0 — name errors must not fetch", hits.Load())
	}
	assertNoScripts(t, r)

	// The same URL works once a name is given.
	if err := r.Add(srv.URL+"/", "rooted", false); err != nil {
		t.Fatalf("Add(url, explicit name): %v", err)
	}
}

func TestAddURLRejectsBadNamesBeforeFetch(t *testing.T) {
	srv, hits := scriptServer(t, map[string]string{"/list": "#!/bin/sh\n", "/ok.sh": "#!/bin/sh\n"})
	r, _ := newTestRunner(t)

	for _, tc := range []struct {
		url, name, wantErr string
	}{
		{srv.URL + "/ok.sh", "../evil", "invalid script name"},
		{srv.URL + "/ok.sh", "a/b", "invalid script name"},
		{srv.URL + "/list", "", "reserved"},
		{srv.URL + "/ok.sh", "alias.sh", "reserved"},
	} {
		err := r.Add(tc.url, tc.name, false)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("Add(%q, %q) error = %v, want %q", tc.url, tc.name, err, tc.wantErr)
		}
	}
	if hits.Load() != 0 {
		t.Errorf("server hits = %d, want 0 — name errors must not fetch", hits.Load())
	}
	assertNoScripts(t, r)
}

func TestAddURLEncodedSeparatorCannotTraverse(t *testing.T) {
	// %2F decodes to "/" in the URL path, so the derived name is only the
	// final segment ("etc") — it can never carry a traversal into the name.
	srv, _ := scriptServer(t, map[string]string{"/../../etc": "#!/bin/sh\necho hi\n"})
	r, _ := newTestRunner(t)

	if err := r.Add(srv.URL+"/..%2F..%2Fetc", "", false); err != nil {
		t.Fatalf("Add(encoded-separator url): %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.Cfg.ScriptsDir, "etc")); err != nil {
		t.Errorf("want script installed as scripts/etc: %v", err)
	}
	entries, err := os.ReadDir(r.Cfg.UCHome)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "scripts" {
			t.Errorf("unexpected file escaped into UCHome: %s", e.Name())
		}
	}
}

func TestAddURLRejectsDuplicateWithoutFetching(t *testing.T) {
	srv, hits := scriptServer(t, map[string]string{
		"/v1/tool.sh": "#!/bin/sh\necho v1\n",
		"/v2/tool.sh": "#!/bin/sh\necho v2\n",
	})
	r, _ := newTestRunner(t)

	if err := r.Add(srv.URL+"/v1/tool.sh", "", false); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	err := r.Add(srv.URL+"/v2/tool.sh", "", false)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate Add(url) error = %v, want already-exists", err)
	}
	if hits.Load() != 1 {
		t.Errorf("server hits = %d, want 1 — duplicate must be refused before fetching", hits.Load())
	}

	dest := filepath.Join(r.Cfg.ScriptsDir, "tool.sh")
	if data, _ := os.ReadFile(dest); !strings.Contains(string(data), "v1") {
		t.Errorf("original script was modified: %q", data)
	}

	if err := r.Add(srv.URL+"/v2/tool.sh", "", true); err != nil {
		t.Fatalf("force Add(url): %v", err)
	}
	if data, _ := os.ReadFile(dest); !strings.Contains(string(data), "v2") {
		t.Errorf("force Add(url) did not replace content: %q", data)
	}
}

func TestAddURLRejectsCrossExtensionAmbiguity(t *testing.T) {
	srv, _ := scriptServer(t, map[string]string{"/tool.py": "print('py')\n"})
	r, _ := newTestRunner(t)
	src := writeScript(t, t.TempDir(), "tool.sh", "#!/bin/sh\necho sh\n")

	if err := r.Add(src, "", false); err != nil {
		t.Fatalf("Add tool.sh: %v", err)
	}
	err := r.Add(srv.URL+"/tool.py", "", false)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("Add(url tool.py) error = %v, want ambiguous", err)
	}
	if err := r.Add(srv.URL+"/tool.py", "", true); err != nil {
		t.Fatalf("force Add(url tool.py): %v", err)
	}
}

func TestAddURLEmptyBody(t *testing.T) {
	srv, _ := scriptServer(t, map[string]string{"/empty.sh": ""})
	r, _ := newTestRunner(t)

	err := r.Add(srv.URL+"/empty.sh", "", false)
	if err == nil || !strings.Contains(err.Error(), "empty response") {
		t.Fatalf("error = %v, want empty-response", err)
	}
	assertNoScripts(t, r)
}

func TestAddURLSizeCap(t *testing.T) {
	atCap := "#!/bin/sh\n" + strings.Repeat("#", maxFetchBytes-len("#!/bin/sh\n"))
	srv, _ := scriptServer(t, map[string]string{
		"/at-cap.sh":   atCap,
		"/over-cap.sh": atCap + "x",
	})
	r, _ := newTestRunner(t)

	err := r.Add(srv.URL+"/over-cap.sh", "", false)
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("over-cap error = %v, want larger-than", err)
	}
	assertNoScripts(t, r)

	if err := r.Add(srv.URL+"/at-cap.sh", "", false); err != nil {
		t.Fatalf("at-cap Add: %v", err)
	}
	info, err := os.Stat(filepath.Join(r.Cfg.ScriptsDir, "at-cap.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != maxFetchBytes {
		t.Errorf("at-cap size = %d, want %d (no truncation)", info.Size(), maxFetchBytes)
	}
}

func TestAddURLConnectionError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	deadURL := srv.URL + "/tool.sh"
	srv.Close()
	r, _ := newTestRunner(t)

	err := r.Add(deadURL, "", false)
	if err == nil || !strings.Contains(err.Error(), "fetch "+deadURL) {
		t.Fatalf("error = %v, want fetch error naming the URL", err)
	}
	assertNoScripts(t, r)
}

func TestAddURLTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	orig := httpClient
	httpClient = &http.Client{Timeout: 50 * time.Millisecond}
	t.Cleanup(func() { httpClient = orig })

	r, _ := newTestRunner(t)
	err := r.Add(srv.URL+"/slow.sh", "", false)
	if err == nil || !strings.Contains(err.Error(), "fetch") {
		t.Fatalf("error = %v, want fetch timeout error", err)
	}
	assertNoScripts(t, r)
}

func TestAddURLInvalidURL(t *testing.T) {
	r, _ := newTestRunner(t)
	err := r.Add("https://", "x", false)
	if err == nil || !strings.Contains(err.Error(), "invalid URL") {
		t.Fatalf("error = %v, want invalid-URL", err)
	}
	assertNoScripts(t, r)
}

func TestAddLocalPathStillWorks(t *testing.T) {
	// A local file whose name merely contains "http" must take the
	// local-copy path, not the fetch path.
	r, out := newTestRunner(t)
	src := writeScript(t, t.TempDir(), "http-check.sh", "#!/bin/sh\necho ok\n")

	if err := r.Add(src, "", false); err != nil {
		t.Fatalf("Add(local): %v", err)
	}
	if strings.Contains(out.String(), "review it") {
		t.Errorf("local add printed the URL review note: %q", out.String())
	}
}

func TestHelpAddMentionsURLForm(t *testing.T) {
	r, out := newTestRunner(t)
	if err := r.Help("add"); err != nil {
		t.Fatalf("Help(add): %v", err)
	}
	for _, want := range []string{"<path|url>", "gist", "raw"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help add = %q, want it to mention %q", out.String(), want)
		}
	}
}

func TestAddURLInstallFailureKeepsExistingAndLeavesNoTemp(t *testing.T) {
	srv, _ := scriptServer(t, map[string]string{"/tool": "#!/bin/sh\necho new\n"})
	r, out := newTestRunner(t)

	// A non-empty directory squatting on the destination makes the final
	// rename fail even under --force; it must survive untouched.
	squat := filepath.Join(r.Cfg.ScriptsDir, "tool")
	if err := os.MkdirAll(squat, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := writeScript(t, squat, "keep", "precious")

	err := r.Add(srv.URL+"/tool", "", true)
	if err == nil || !strings.Contains(err.Error(), "write "+squat) {
		t.Fatalf("error = %v, want write error for %s", err, squat)
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "precious" {
		t.Errorf("existing destination was disturbed: %q, %v", data, err)
	}
	if strings.Contains(out.String(), "added") {
		t.Errorf("output = %q, want no success line", out.String())
	}
	entries, err := os.ReadDir(r.Cfg.ScriptsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "tool" {
			t.Errorf("leftover file in scripts dir: %s", e.Name())
		}
	}
}

func TestAddLeavesNoTempFiles(t *testing.T) {
	srv, _ := scriptServer(t, map[string]string{"/remote.sh": "#!/bin/sh\necho remote\n"})
	r, _ := newTestRunner(t)
	local := writeScript(t, t.TempDir(), "local.sh", "#!/bin/sh\necho local\n")

	if err := r.Add(local, "", false); err != nil {
		t.Fatalf("Add(local): %v", err)
	}
	if err := r.Add(srv.URL+"/remote.sh", "", false); err != nil {
		t.Fatalf("Add(url): %v", err)
	}
	if err := r.Add(local, "", true); err != nil {
		t.Fatalf("force Add(local): %v", err)
	}

	entries, err := os.ReadDir(r.Cfg.ScriptsDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"local.sh", "remote.sh"}) {
		t.Errorf("scripts dir = %v, want exactly [local.sh remote.sh]", names)
	}
	for _, n := range names {
		info, err := os.Stat(filepath.Join(r.Cfg.ScriptsDir, n))
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
			t.Errorf("%s mode = %v, want 0755", n, info.Mode().Perm())
		}
	}
}

func TestHelpSync(t *testing.T) {
	r, out := newTestRunner(t)
	if err := r.Help("sync"); err != nil {
		t.Fatalf("Help(sync): %v", err)
	}
	for _, want := range []string{"uc sync init <remote-url>", "PRIVATE", "history.json", "--force"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help sync = %q, want it to mention %q", out.String(), want)
		}
	}

	out.Reset()
	if err := r.Help(""); err != nil {
		t.Fatalf("Help(): %v", err)
	}
	if !strings.Contains(out.String(), "uc sync push") {
		t.Errorf("overall help = %q, want the sync commands listed", out.String())
	}
}
