package commands

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/eftakhairul/uc/internal/config"
	"github.com/eftakhairul/uc/internal/help"
	"github.com/eftakhairul/uc/internal/history"
	"github.com/eftakhairul/uc/internal/registry"
	"github.com/eftakhairul/uc/internal/store"
)

// errBuf returns the runner's captured warning stream.
func errBuf(r *Runner) *bytes.Buffer { return r.Err.(*bytes.Buffer) }

func TestListEmpty(t *testing.T) {
	r, out := newTestRunner(t)
	if err := r.List(); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "Nothing registered yet — see 'uc help' to get started\n" {
		t.Errorf("List() = %q", got)
	}
}

func TestHelpOverviewIsHelpPackageOverview(t *testing.T) {
	r, out := newTestRunner(t)
	if err := r.Help(""); err != nil {
		t.Fatal(err)
	}
	if out.String() != help.Overview() {
		t.Error("Help(\"\") differs from help.Overview()")
	}
}

func TestHelpShortAlias(t *testing.T) {
	r, out := newTestRunner(t)
	if err := r.Help("rm"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "usage: uc remove <name>\n   or: uc rm <name>") {
		t.Errorf("help rm = %q", out.String())
	}
}

// `uc help <unknown>` is an error with suggestions, but not the 127
// NotFoundError `uc <unknown>` returns: nothing was meant to run.
func TestHelpUnknownIsAnError(t *testing.T) {
	r, out := newTestRunner(t)
	if err := r.AliasAdd("killport", "echo kill", ""); err != nil {
		t.Fatal(err)
	}
	err := r.Help("kilport")
	if err == nil {
		t.Fatal("want an error")
	}
	want := "'kilport' is not a uc command or a registered name.\n\nThe most similar name is\n\tkillport"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err, want)
	}
	if _, ok := errors.AsType[*registry.NotFoundError](err); ok {
		t.Error("Help returned *NotFoundError, which main maps to exit 127")
	}
	if strings.Contains(out.String(), "No help available") {
		t.Errorf("stdout = %q", out.String())
	}

	err = r.Help("xyzzy")
	if err == nil || err.Error() != "'xyzzy' is not a uc command or a registered name — see 'uc list'" {
		t.Errorf("err = %v", err)
	}
}

func TestNotFoundSuggestions(t *testing.T) {
	r, _ := newTestRunner(t)
	for _, name := range []string{"killport", "deploy"} {
		if err := r.AliasAdd(name, "echo "+name, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name string
		want []string
	}{
		{"kilport", []string{"killport"}},
		{"dep", []string{"deploy"}},       // prefix
		{"lst", []string{"list", "ls"}},   // reserved, both distance 1
		{"xyzzy", nil},                    // nothing close
		{"ad", []string{"add"}},           // prefix
		{"al", []string{"alias"}},         // prefix
		{"kilport", []string{"killport"}}, // distance 2 'import' loses to distance 1
		{"ab", nil},                       // short: distance 1 only, no prefix hit
		{"remov", []string{"remove"}},     // distance 1
		{"histroy", []string{"history"}},  // transposition = 2
		{"syncc", []string{"sync"}},       // distance 1
		{"killport", nil},                 // exact names are not suggestions
	} {
		got := r.suggest(c.name)
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("suggest(%q) = %q, want %q", c.name, got, c.want)
		}
	}

	err := r.Which("kilport")
	nf, ok := errors.AsType[*registry.NotFoundError](err)
	if !ok || len(nf.Similar) != 1 || nf.Similar[0] != "killport" {
		t.Errorf("Which(kilport) err = %v", err)
	}
}

func TestSuggestCapsAtThree(t *testing.T) {
	r, _ := newTestRunner(t)
	for _, n := range []string{"tool1", "tool2", "tool3", "tool4"} {
		if err := r.AliasAdd(n, "echo", ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.suggest("tool"); len(got) != 3 {
		t.Errorf("suggest(tool) = %q, want 3", got)
	}
}

func TestLevenshtein(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"", "", 0}, {"a", "", 1}, {"", "abc", 3}, {"kitten", "sitting", 3},
		{"kilport", "killport", 1}, {"histroy", "history", 2}, {"héllo", "hello", 1},
	} {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// improvement spec §4.1: lookups never escape the scripts directory.
func TestRemoveTraversalDeletesNothing(t *testing.T) {
	r, _ := newTestRunner(t)
	if err := r.Cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(r.Cfg.UCHome, "victim")
	if err := os.WriteFile(victim, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../victim", "../../victim", "./../victim"} {
		if err := r.Remove(name); err == nil {
			t.Errorf("Remove(%q) succeeded", name)
		}
		if err := r.Which(name); err == nil {
			t.Errorf("Which(%q) succeeded", name)
		}
	}
	if _, err := os.Stat(victim); err != nil {
		t.Errorf("file outside the scripts dir was touched: %v", err)
	}
}

func TestAliasAndFunctionAddRejectBadNames(t *testing.T) {
	r, _ := newTestRunner(t)
	for _, name := range []string{"a b", "", "-x", "../x", "list", "a\nb"} {
		if err := r.AliasAdd(name, "echo", ""); err == nil {
			t.Errorf("AliasAdd(%q) succeeded", name)
		}
		if err := r.FunctionAdd(name, "echo", ""); err == nil {
			t.Errorf("FunctionAdd(%q) succeeded", name)
		}
	}
	if items, _ := r.Reg.List(); len(items) != 0 {
		t.Errorf("registered %v", items)
	}
}

// improvement spec §4.3.
func TestEmptyAliasAndFunctionRejected(t *testing.T) {
	r, _ := newTestRunner(t)
	for _, text := range []string{"", "  ", "\n"} {
		if err := r.AliasAdd("e", text, ""); err == nil || err.Error() != "the alias command cannot be empty" {
			t.Errorf("AliasAdd(%q) = %v", text, err)
		}
		if err := r.FunctionAdd("f", text, ""); err == nil || err.Error() != "the function body cannot be empty" {
			t.Errorf("FunctionAdd(%q) = %v", text, err)
		}
	}
}

func TestEditToEmptySavesNothing(t *testing.T) {
	r, out := newTestRunner(t)
	fakeEditor(t, `printf '   \n' > "$1"`)
	if err := r.AliasAdd("gs", "git status", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.FunctionAdd("ship", "uc build", ""); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := r.AliasEdit("gs"); err != nil {
		t.Fatal(err)
	}
	if err := r.FunctionEdit("ship"); err != nil {
		t.Fatal(err)
	}
	if strings.Count(out.String(), "aborted, nothing saved") != 2 {
		t.Errorf("out = %q", out.String())
	}
	f, _ := r.Reg.Store.Load()
	if f.Aliases["gs"].Command != "git status" || f.Functions["ship"].Body != "uc build" {
		t.Errorf("entries changed: %+v", f)
	}
}

func TestFunctionEditDropsTrailingNewline(t *testing.T) {
	r, _ := newTestRunner(t)
	fakeEditor(t, `printf 'echo one\necho two\n\n' > "$1"`)
	if err := r.FunctionAdd("f", "echo", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.FunctionEdit("f"); err != nil {
		t.Fatal(err)
	}
	f, _ := r.Reg.Store.Load()
	if got := f.Functions["f"].Body; got != "echo one\necho two" {
		t.Errorf("body = %q", got)
	}
}

func TestEditorErrors(t *testing.T) {
	r, _ := newTestRunner(t)
	if err := r.AliasAdd("gs", "git status", ""); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", "no-such-editor-xyz")
	err := r.AliasEdit("gs")
	if err == nil || err.Error() != `editor 'no-such-editor-xyz' not found — set "editor" in settings.json, or $EDITOR` {
		t.Errorf("missing editor err = %v", err)
	}

	t.Setenv("EDITOR", `"unclosed`)
	if err := r.AliasEdit("gs"); err == nil || !strings.Contains(err.Error(), "cannot parse the editor command") {
		t.Errorf("unparseable editor err = %v", err)
	}

	fakeEditor(t, "exit 3")
	err = r.AliasEdit("gs")
	if err == nil || err.Error() != "editor exited with an error, nothing saved" {
		t.Errorf("failing editor err = %v", err)
	}
}

// An editor path with spaces works when quoted (improvement spec §4.12e).
func TestEditorPathWithSpaces(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell stub")
	}
	r, _ := newTestRunner(t)
	dir := filepath.Join(t.TempDir(), "My Editor")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	editor := filepath.Join(dir, "ed it")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf 'edited' > \"$2\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", `"`+editor+`" --wait`)
	if err := r.AliasAdd("gs", "git status", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.AliasEdit("gs"); err != nil {
		t.Fatal(err)
	}
	f, _ := r.Reg.Store.Load()
	if f.Aliases["gs"].Command != "edited" {
		t.Errorf("command = %q", f.Aliases["gs"].Command)
	}
}

func TestAliasAddDescNote(t *testing.T) {
	for _, c := range []struct {
		name     string
		words    []string
		desc     string
		literal  bool
		wantNote bool
		wantLine string
	}{
		{"trailing", []string{"git", "status", "--desc", "short status"}, "", false, true, `          uc alias add gs --desc "short status" git status`},
		{"middle", []string{"mytool", "create", "--desc", "x", "--verbose"}, "", false, true, ""},
		{"desc given", []string{"git", "status", "--desc", "x"}, "real", false, false, ""},
		{"literal", []string{"git", "status", "--desc", "x"}, "", true, false, ""},
		{"no desc word", []string{"git", "status"}, "", false, false, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, out := newTestRunner(t)
			if err := r.AliasAddWords("gs", c.words, c.desc, c.literal); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), "added alias gs -> "+strings.Join(c.words, " ")) {
				t.Errorf("out = %q", out.String())
			}
			note := errBuf(r).String()
			if got := strings.Contains(note, "'--desc' is part of the alias command"); got != c.wantNote {
				t.Errorf("note shown = %v, want %v: %q", got, c.wantNote, note)
			}
			if c.wantLine != "" && !strings.Contains(note, c.wantLine+"\n") {
				t.Errorf("note = %q, want suggestion %q", note, c.wantLine)
			}
			if c.wantNote && c.wantLine == "" && strings.Contains(note, "uc alias add gs --desc") {
				t.Errorf("mid-command --desc must not get a suggestion line: %q", note)
			}
		})
	}
}

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"git":          "git",
		"short status": `"short status"`,
		`say "hi"`:     `"say \"hi\""`,
		"$HOME":        `"\$HOME"`,
		"":             `""`,
		"a&&b":         `"a&&b"`,
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestWhichEscapesBody(t *testing.T) {
	r, out := newTestRunner(t)
	if err := r.AliasAdd("x", "printf '\x1b[2J'", ""); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := r.Which("x"); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != `x (alias): printf '\x1b[2J'`+"\n" {
		t.Errorf("which = %q", got)
	}
}

func TestListStripsEscapes(t *testing.T) {
	r, out := newTestRunner(t)
	if err := r.Cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	writeScript(t, r.Cfg.ScriptsDir, "esc", "#!/bin/sh\n# @desc: \x1b[31mRED\x1b[0m\n")
	if err := r.List(); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(out.String(), 0x1b) || !strings.Contains(out.String(), "[31mRED[0m") {
		t.Errorf("list = %q", out.String())
	}
}

func TestHistoryOff(t *testing.T) {
	r, out := newTestRunner(t)
	r.Cfg.Settings.HistorySize = new(0)
	r.recordHistory("x", registry.KindScript, nil)
	r.recordHistory("x", registry.KindScript, nil)
	if _, err := os.Stat(r.Cfg.HistoryPath()); !os.IsNotExist(err) {
		t.Errorf("history written with history off: %v", err)
	}
	if err := r.History(0); err != nil {
		t.Fatal(err)
	}
	if out.String() != "history is turned off (history_size is 0)\n" {
		t.Errorf("history = %q", out.String())
	}
	if err := r.HistoryRun(1); err == nil || err.Error() != "history is turned off (history_size is 0)" {
		t.Errorf("HistoryRun = %v", err)
	}
}

func TestHistoryRunOutOfRange(t *testing.T) {
	r, _ := newTestRunner(t)
	if err := r.HistoryRun(1); err == nil || err.Error() != "no history entry #1 — history is empty" {
		t.Errorf("empty: %v", err)
	}
	for range 4 {
		r.recordHistory("x", registry.KindAlias, nil)
	}
	if err := r.HistoryRun(9); err == nil || err.Error() != "no history entry #9 — there are 4" {
		t.Errorf("out of range: %v", err)
	}
}

func TestHistoryClear(t *testing.T) {
	r, out := newTestRunner(t)
	r.recordHistory("deploy", registry.KindScript, []string{"--token=abc"})
	if err := r.HistoryClear(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "history cleared") {
		t.Errorf("out = %q", out.String())
	}
	entries, err := history.Load(r.Cfg.HistoryPath())
	if err != nil || len(entries) != 0 {
		t.Errorf("entries = %v, %v", entries, err)
	}
}

// improvement spec §4.8.
func TestAddHTTPRefused(t *testing.T) {
	r, _ := newTestRunner(t)
	err := r.Add("http://example.invalid/x.sh", "", false, false)
	if err == nil || err.Error() != "refusing to download over plain http — use an https URL, or pass --insecure" {
		t.Errorf("err = %v", err)
	}
	assertNoScripts(t, r)
}

func TestAddHTTPInsecureAllowed(t *testing.T) {
	r, _ := newTestRunner(t)
	orig := httpTimeout
	httpTimeout = 2 * time.Second
	t.Cleanup(func() { httpTimeout = orig })
	err := r.Add("http://example.invalid/x.sh", "", false, true)
	if err == nil || !strings.HasPrefix(err.Error(), "cannot download http://example.invalid/x.sh") {
		t.Errorf("--insecure must get as far as the download, err = %v", err)
	}
}

func TestIsLoopback(t *testing.T) {
	for host, want := range map[string]bool{
		"localhost": true, "LOCALHOST": true, "127.0.0.1": true, "127.9.9.9": true, "::1": true,
		"example.com": false, "10.0.0.1": false, "": false, "localhost.evil.com": false,
	} {
		if got := isLoopback(host); got != want {
			t.Errorf("isLoopback(%q) = %v", host, got)
		}
	}
}

func TestAddRedirectDowngradeRefused(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "http://example.invalid/x.sh", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	httpTransport = srv.Client().Transport
	t.Cleanup(func() { httpTransport = nil })

	r, _ := newTestRunner(t)
	err := r.Add(srv.URL+"/x.sh", "", false, false)
	if err == nil || !strings.Contains(err.Error(), "redirected to plain http — pass --insecure to allow") {
		t.Errorf("err = %v", err)
	}
	assertNoScripts(t, r)
}

func TestAddTooManyRedirects(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, srv.URL+"/loop.sh", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	r, _ := newTestRunner(t)
	err := r.Add(srv.URL+"/loop.sh", "", false, false)
	if err == nil || !strings.Contains(err.Error(), "too many redirects") {
		t.Errorf("err = %v", err)
	}
}

func TestAddHTMLRefused(t *testing.T) {
	for _, body := range []string{"<!DOCTYPE html><html></html>", "\n  <html lang=en>", "\xef\xbb\xbf<!doctype html>"} {
		srv, _ := scriptServer(t, map[string]string{"/page.sh": body})
		r, _ := newTestRunner(t)
		err := r.Add(srv.URL+"/page.sh", "", false, false)
		if err == nil || err.Error() != "this URL returned a web page, not a script — use the raw file URL" {
			t.Errorf("body %q: err = %v", body, err)
		}
		assertNoScripts(t, r)
	}
}

func TestAddFileErrors(t *testing.T) {
	r, _ := newTestRunner(t)
	err := r.Add("/no/such/file.sh", "", false, false)
	if err == nil || err.Error() != "cannot add '/no/such/file.sh': no such file" {
		t.Errorf("missing file: %v", err)
	}
	dir := t.TempDir()
	if err := r.Add(dir, "x", false, false); err == nil || err.Error() != "cannot add '"+dir+"': it is a directory" {
		t.Errorf("directory: %v", err)
	}
}

// improvement spec §4.12c.
func TestAddRefusesToShadowAliasOrFunction(t *testing.T) {
	r, _ := newTestRunner(t)
	if err := r.AliasAdd("gs", "git status", ""); err != nil {
		t.Fatal(err)
	}
	src := writeScript(t, t.TempDir(), "gs.sh", "#!/bin/sh\n")
	err := r.Add(src, "", false, false)
	if err == nil || err.Error() != "'gs' already exists (alias) — remove it first, or pass --force" {
		t.Errorf("err = %v", err)
	}
	if err := r.Add(src, "", true, false); err != nil {
		t.Fatalf("--force: %v", err)
	}
	if !strings.Contains(errBuf(r).String(), "the script shadows the alias 'gs'") {
		t.Errorf("warn = %q", errBuf(r).String())
	}
}

func TestEnsureDirsArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	r, _ := newTestRunner(t)
	src := writeScript(t, t.TempDir(), "x.sh", "#!/bin/sh\n")
	if err := r.Add(src, "", false, false); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{r.Cfg.UCHome, r.Cfg.ScriptsDir} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != config.DirMode {
			t.Errorf("%s mode = %o, want %o", dir, info.Mode().Perm(), config.DirMode)
		}
	}
	if err := r.AliasAdd("gs", "git status", ""); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(r.Cfg.ConfigDir); info.Mode().Perm() != config.DirMode {
		t.Errorf("config dir mode = %o", info.Mode().Perm())
	}
}

// Reading must never migrate: only the first write does.
func TestReadOnlyCommandsDoNotMigrate(t *testing.T) {
	r, _ := newTestRunner(t)
	if err := os.MkdirAll(r.Cfg.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := `{"gs": {"command": "git status", "desc": "short", "usage": null, "examples": null}}`
	if err := os.WriteFile(r.Cfg.AliasesPath(), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, fn := range map[string]func() error{
		"list":     r.List,
		"which":    func() error { return r.Which("gs") },
		"help":     func() error { return r.Help("gs") },
		"complete": func() error { return r.Complete("g", true) },
		"alias ls": r.AliasList,
		"history":  func() error { return r.History(0) },
	} {
		if err := fn(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := os.Stat(r.Cfg.CommandsPath()); !os.IsNotExist(err) {
		t.Error("a read-only command created commands.json")
	}
	if data, _ := os.ReadFile(r.Cfg.AliasesPath()); string(data) != legacy {
		t.Error("a read-only command changed aliases.json")
	}

	// The first write migrates.
	if err := r.FunctionAdd("ship", "uc deploy", ""); err != nil {
		t.Fatal(err)
	}
	f, err := r.Reg.Store.Load()
	if err != nil || f.Kind("gs") != "alias" || f.Kind("ship") != "function" {
		t.Errorf("after migration: %+v, %v", f, err)
	}
}

func TestAliasFunctionShareNamespace(t *testing.T) {
	r, _ := newTestRunner(t)
	if err := r.FunctionAdd("ship", "uc deploy", ""); err != nil {
		t.Fatal(err)
	}
	err := r.AliasAdd("ship", "echo", "")
	if err == nil || err.Error() != "'ship' is already a function — remove it first with 'uc remove ship'" {
		t.Errorf("err = %v", err)
	}
}

var _ = store.Version
