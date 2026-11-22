package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eftakhairul/uc/internal/commands"
	"github.com/eftakhairul/uc/internal/config"
	"github.com/eftakhairul/uc/internal/help"
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
	r.Err = &bytes.Buffer{}
	return r, out
}

// home points uc at a fresh UC_HOME and config dir and returns them.
type home struct {
	ucHome, configDir string
}

func newHome(t *testing.T) home {
	t.Helper()
	root := t.TempDir()
	h := home{ucHome: filepath.Join(root, "uc-home"), configDir: filepath.Join(root, "config")}
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	t.Setenv("UC_HOME", h.ucHome)
	t.Setenv("XDG_CONFIG_HOME", h.configDir)
	return h
}

// uc runs the CLI in-process. Never use it to run a script: on unix a
// successful run replaces the test process.
func uc(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(args, strings.NewReader(""), &out, &errb)
	return out.String(), errb.String(), code
}

// stateOf lists everything under the uc dirs, to prove nothing was written.
func stateOf(t *testing.T, h home) string {
	t.Helper()
	var b strings.Builder
	for _, root := range []string{h.ucHome, h.configDir} {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			data := ""
			if !info.IsDir() {
				d, _ := os.ReadFile(p)
				data = string(d)
			}
			fmt.Fprintf(&b, "%s=%s\n", p, data)
			return nil
		})
	}
	return b.String()
}

// usageCase is one bad invocation and the first line it must print.
type usageCase struct {
	args  []string
	first string // first stderr line, without "uc: "
	cmd   string // the command whose help is pointed at ("" = top level)
}

var usageCases = []usageCase{
	// improvement spec §3.1, row by row.
	{[]string{"history", "-n"}, "'history' expects a number, got '-n'", "history"},
	{[]string{"history", "abc"}, "'history' expects a number, got 'abc'", "history"},
	{[]string{"history", "run", "x"}, "'history run' expects a number, got 'x'", "history"},
	{[]string{"history", "-5"}, "'history' expects a number, got '-5'", "history"},
	{[]string{"history", "0"}, "'history' expects a number, got '0'", "history"},
	{[]string{"history", "3", "junk"}, "unexpected argument 'junk'", "history"},
	{[]string{"list", "junk"}, "unexpected argument 'junk'", "list"},
	{[]string{"ls", "junk"}, "unexpected argument 'junk'", "ls"},
	{[]string{"version", "junk"}, "unexpected argument 'junk'", "version"},
	{[]string{"help", "a", "b"}, "unexpected argument 'b'", "help"},
	{[]string{"alias", "list", "junk"}, "unexpected argument 'junk'", "alias"},
	{[]string{"function", "add", "f", "echo", "hi"}, "the function body must be one argument — wrap it in quotes", "function"},
	{[]string{"function", "add", "g", "echo hi", "--desc"}, "option '--desc' needs a value", "function"},
	{[]string{"--foo"}, "unknown option '--foo'", ""},
	{[]string{"-"}, "unknown option '-'", ""},
	{[]string{"remove", "--all"}, "unknown option '--all'", "remove"},
	{[]string{"add", "--froce", "x"}, "unknown option '--froce'", "add"},
	// Missing and surplus arguments, one per command.
	{[]string{"add"}, "missing argument", "add"},
	{[]string{"add", "a", "b", "c"}, "unexpected argument 'c'", "add"},
	{[]string{"add", "x", "--force=yes"}, "option '--force' takes no value", "add"},
	{[]string{"new"}, "missing argument", "new"},
	{[]string{"new", "x", "--lang"}, "option '--lang' needs a value", "new"},
	{[]string{"new", "x", "--lang", "py", "--lang", "sh"}, "option '--lang' given more than once", "new"},
	{[]string{"remove"}, "missing argument", "remove"},
	{[]string{"rm", "a", "b"}, "unexpected argument 'b'", "rm"},
	{[]string{"which"}, "missing argument", "which"},
	{[]string{"which", "-x"}, "unknown option '-x'", "which"},
	{[]string{"edit", "a", "b"}, "unexpected argument 'b'", "edit"},
	{[]string{"completion"}, "missing argument", "completion"},
	{[]string{"completion", "pwsh"}, "unsupported shell 'pwsh' (want bash, zsh, or fish)", "completion"},
	{[]string{"history", "run"}, "missing argument", "history"},
	{[]string{"history", "run", "1", "2"}, "unexpected argument '2'", "history"},
	{[]string{"history", "clear", "now"}, "unexpected argument 'now'", "history"},
	{[]string{"alias"}, "'alias' needs a command: add, remove, list, or edit", "alias"},
	{[]string{"alias", "ad", "x"}, "unknown alias command 'ad'", "alias"},
	{[]string{"alias", "add"}, "missing argument", "alias"},
	{[]string{"alias", "add", "x"}, "missing argument", "alias"},
	{[]string{"alias", "add", "x", "--desc"}, "option '--desc' needs a value", "alias"},
	{[]string{"alias", "add", "x", "--desc", "d"}, "missing argument", "alias"},
	{[]string{"alias", "add", "x", "--help"}, "unknown option '--help'", "alias"},
	{[]string{"alias", "add", "x", "--desc", "a", "--desc", "b", "ls"}, "option '--desc' given more than once", "alias"},
	{[]string{"alias", "add", "--desc", "d", "x", "ls"}, "unknown option '--desc'", "alias"},
	{[]string{"alias", "remove"}, "missing argument", "alias"},
	{[]string{"alias", "edit", "a", "b"}, "unexpected argument 'b'", "alias"},
	{[]string{"function"}, "'function' needs a command: add, remove, list, or edit", "function"},
	{[]string{"function", "add", "f"}, "missing argument", "function"},
	{[]string{"function", "list", "x"}, "unexpected argument 'x'", "function"},
	{[]string{"sync"}, "'sync' needs a command: init, push, pull, or status", "sync"},
	{[]string{"sync", "bogus"}, "unknown sync command 'bogus'", "sync"},
	{[]string{"sync", "init"}, "missing argument", "sync"},
	{[]string{"sync", "init", "a", "b"}, "unexpected argument 'b'", "sync"},
	{[]string{"sync", "init", "--upload-pack=touch /tmp/x"}, "unknown option '--upload-pack'", "sync"},
	{[]string{"sync", "push", "x"}, "unexpected argument 'x'", "sync"},
	{[]string{"sync", "pull", "--force", "x"}, "unexpected argument 'x'", "sync"},
	{[]string{"sync", "status", "--force"}, "unknown option '--force'", "sync"},
	{[]string{"export", "a", "b"}, "unexpected argument 'b'", "export"},
	{[]string{"export", "--dry-run"}, "unknown option '--dry-run'", "export"},
	{[]string{"import"}, "missing argument", "import"},
	{[]string{"import", "a", "--frce"}, "unknown option '--frce'", "import"},
	{[]string{"--help", "x"}, "unexpected argument 'x'", ""},
	{[]string{"--version", "x"}, "unexpected argument 'x'", ""},
}

func TestUsageErrors(t *testing.T) {
	for _, c := range usageCases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			h := newHome(t)
			before := stateOf(t, h)
			stdout, stderr, code := uc(t, c.args...)
			if code != 2 {
				t.Errorf("exit = %d, want 2\nstderr: %s", code, stderr)
			}
			lines := strings.Split(strings.TrimRight(stderr, "\n"), "\n")
			if lines[0] != "uc: "+c.first {
				t.Errorf("first line = %q, want %q", lines[0], "uc: "+c.first)
			}
			usage, _ := help.Usage(c.cmd)
			if !strings.Contains(stderr, "\n"+usage+"\n") {
				t.Errorf("stderr lacks the usage block for %q:\n%s", c.cmd, stderr)
			}
			see := "See 'uc help'."
			if c.cmd != "" {
				see = "See 'uc help " + c.cmd + "'."
			}
			if lines[len(lines)-1] != see {
				t.Errorf("last line = %q, want %q", lines[len(lines)-1], see)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
			if after := stateOf(t, h); after != before {
				t.Errorf("a usage error wrote files:\n%s", after)
			}
		})
	}
}

var goInternals = []string{"strconv.", "invalid syntax", "json: cannot", "invalid character", "unexpected end of JSON", "Go struct", "exec:", "exit status", "syscall", "stat ", "open /"}

// Errors are for people: no Go error text, ever (improvement spec §3.2).
func TestNoGoInternalsInErrors(t *testing.T) {
	check := func(t *testing.T, args []string, stderr string) {
		t.Helper()
		for _, bad := range goInternals {
			if strings.Contains(stderr, bad) {
				t.Errorf("uc %q leaks %q:\n%s", args, bad, stderr)
			}
		}
	}
	for _, c := range usageCases {
		newHome(t)
		_, stderr, _ := uc(t, c.args...)
		check(t, c.args, stderr)
	}

	h := newHome(t)
	for _, args := range [][]string{
		{"add", "/no/such/file.sh"},
		{"add", t.TempDir(), "x"},
		{"import", "/no/such.tar.gz"},
		{"which", "nope"},
		{"remove", "../escape"},
		{"history", "run", "3"},
		{"help", "nope"},
		{"sync", "push"},
		{"alias", "edit", "nope"},
	} {
		_, stderr, code := uc(t, args...)
		if code == 0 {
			t.Errorf("uc %q succeeded", args)
		}
		check(t, args, stderr)
	}

	// Broken commands.json.
	if err := os.MkdirAll(h.configDir+"/uc", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.configDir, "uc", "commands.json"), []byte("{\n  \"aliases\": {,\n}"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := uc(t, "list")
	if code != 1 || !strings.Contains(stderr, "uc: cannot read commands.json: invalid JSON at line 2") {
		t.Errorf("broken commands.json: exit %d, stderr %q", code, stderr)
	}
	check(t, []string{"list"}, stderr)
	// Bare uc reads the same store, so it must fail exactly like uc list —
	// not fall back to the help overview as if nothing were registered.
	bareOut, bareErr, bareCode := uc(t)
	if bareCode != code || bareErr != stderr || bareOut != "" {
		t.Errorf("bare uc, broken commands.json: exit %d, stdout %q, stderr %q; want uc list's exit %d, stderr %q", bareCode, bareOut, bareErr, code, stderr)
	}

	// An editor that doesn't exist.
	t.Setenv("EDITOR", "no-such-editor-xyz")
	if err := os.WriteFile(filepath.Join(h.configDir, "uc", "commands.json"), []byte(`{"aliases": {"gs": {"command": "git status"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stderr, _ = uc(t, "alias", "edit", "gs")
	if !strings.Contains(stderr, `editor 'no-such-editor-xyz' not found`) {
		t.Errorf("missing editor: %q", stderr)
	}
	check(t, []string{"alias", "edit"}, stderr)
}

// A broken settings.json is a warning, never a lockout (improvement spec
// §3.6) — and tab completion stays silent about it.
func TestBrokenSettingsWarnsAndRuns(t *testing.T) {
	h := newHome(t)
	if err := os.MkdirAll(filepath.Join(h.configDir, "uc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.configDir, "uc", "settings.json"), []byte(`{"history_size": "x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := uc(t, "help")
	if code != 0 || stdout != help.Overview() {
		t.Errorf("help with broken settings: exit %d", code)
	}
	if stderr != "uc: warning: ignoring settings.json: \"history_size\" must be a whole number — using defaults\n" {
		t.Errorf("stderr = %q", stderr)
	}
	_, stderr, code = uc(t, "__complete", "l")
	if code != 0 || stderr != "" {
		t.Errorf("__complete: exit %d, stderr %q", code, stderr)
	}
}

func TestHelpFlags(t *testing.T) {
	newHome(t)
	overview, _, code := uc(t, "help")
	if code != 0 || overview != help.Overview() {
		t.Fatalf("uc help: exit %d", code)
	}
	for _, args := range [][]string{{"-h"}, {"--help"}} {
		if out, _, code := uc(t, args...); code != 0 || out != overview {
			t.Errorf("uc %v: exit %d, output differs from uc help", args, code)
		}
	}
	// Bare uc with nothing registered shows the same overview.
	if out, _, code := uc(t); code != 0 || out != overview {
		t.Errorf("bare uc: exit %d, output %q", code, out)
	}

	// Once something is registered, bare uc prints exactly what uc list does.
	if _, errOut, code := uc(t, "alias", "add", "gs", "git", "status"); code != 0 {
		t.Fatalf("alias add: exit %d, stderr %q", code, errOut)
	}
	list, _, code := uc(t, "list")
	if code != 0 || !strings.Contains(list, "gs") {
		t.Fatalf("uc list: exit %d, output %q", code, list)
	}
	if out, errOut, code := uc(t); code != 0 || out != list || errOut != "" {
		t.Errorf("bare uc with items: exit %d, stdout %q, stderr %q; want uc list output %q", code, out, errOut, list)
	}

	helpAlias, _, _ := uc(t, "help", "alias")
	for _, args := range [][]string{{"alias", "--help"}, {"alias", "-h"}} {
		if out, _, code := uc(t, args...); code != 0 || out != helpAlias {
			t.Errorf("uc %v: exit %d, want uc help alias", args, code)
		}
	}
	helpRm, _, _ := uc(t, "help", "remove")
	if out, _, _ := uc(t, "rm", "--help"); out != helpRm {
		t.Error("uc rm --help differs from uc help remove")
	}

	version, _, _ := uc(t, "version")
	for _, args := range [][]string{{"-v"}, {"--version"}} {
		if out, _, code := uc(t, args...); code != 0 || out != version {
			t.Errorf("uc %v: exit %d, output %q", args, code, out)
		}
	}
}

func TestNotFoundExitCodes(t *testing.T) {
	newHome(t)
	if _, _, code := uc(t, "alias", "add", "killport", "echo", "kill"); code != 0 {
		t.Fatal("alias add")
	}
	_, stderr, code := uc(t, "kilport")
	if code != 127 {
		t.Errorf("uc kilport: exit %d, want 127", code)
	}
	if stderr != "uc: 'kilport' is not a uc command or a registered name.\n\nThe most similar name is\n\tkillport\n" {
		t.Errorf("stderr = %q", stderr)
	}
	// Nothing was meant to run, so help on an unknown name exits 1.
	_, stderr2, code := uc(t, "help", "kilport")
	if code != 1 || stderr2 != stderr {
		t.Errorf("uc help kilport: exit %d, stderr %q", code, stderr2)
	}
	if _, _, code := uc(t, "kilport", "--help"); code != 1 {
		t.Errorf("uc kilport --help: exit %d, want 1", code)
	}
}

func TestListEmptyMessage(t *testing.T) {
	newHome(t)
	out, _, code := uc(t, "list")
	if code != 0 || out != "Nothing registered yet — see 'uc help' to get started\n" {
		t.Errorf("exit %d, out %q", code, out)
	}
}

func TestAliasAddParsing(t *testing.T) {
	for _, c := range []struct {
		args    []string
		name    string
		command string
		desc    string
		literal bool
		err     string
	}{
		{args: []string{"gs", "git", "status"}, name: "gs", command: "git status"},
		{args: []string{"gs", "--desc", "short status", "git", "status"}, name: "gs", command: "git status", desc: "short status"},
		{args: []string{"mk", "mytool", "create", "--desc", "x", "--verbose"}, name: "mk", command: "mytool create --desc x --verbose"},
		{args: []string{"gs", "git", "status", "--desc", "short status"}, name: "gs", command: "git status --desc short status"},
		{args: []string{"x", "ls", "--desc"}, name: "x", command: "ls --desc"},
		{args: []string{"x", "--", "--weird-binary", "-v"}, name: "x", command: "--weird-binary -v", literal: true},
		{args: []string{"x", "--desc", "d", "--", "-v"}, name: "x", command: "-v", desc: "d", literal: true},
		{args: []string{"x", "--", "--desc", "y"}, name: "x", command: "--desc y", literal: true},
		{args: []string{"gs", "git status"}, name: "gs", command: "git status"},
		{args: []string{"x", "--help"}, err: "unknown option '--help'"},
		{args: []string{"x", "--desc"}, err: "option '--desc' needs a value"},
		{args: []string{"x", "--desc", "d"}, err: "missing argument"},
		{args: []string{"x", "--"}, err: "missing argument"},
		{args: []string{"x"}, err: "missing argument"},
		{args: nil, err: "missing argument"},
	} {
		name, words, desc, literal, err := parseAliasAdd(c.args)
		if c.err != "" {
			if err == nil || err.Error() != c.err {
				t.Errorf("parseAliasAdd(%q) err = %v, want %q", c.args, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseAliasAdd(%q): %v", c.args, err)
			continue
		}
		if name != c.name || strings.Join(words, " ") != c.command || desc != c.desc || literal != c.literal {
			t.Errorf("parseAliasAdd(%q) = %q %q %q %v, want %q %q %q %v", c.args, name, strings.Join(words, " "), desc, literal, c.name, c.command, c.desc, c.literal)
		}
	}
}

func TestSplitFlags(t *testing.T) {
	spec := flagSpec{"--force": false, "--lang": true}
	for _, c := range []struct {
		args  []string
		flags string
		pos   string
		err   string
	}{
		{args: []string{"a", "--force", "b"}, flags: "--force=", pos: "a,b"},
		{args: []string{"--lang", "py", "x"}, flags: "--lang=py", pos: "x"},
		{args: []string{"--lang=py", "x"}, flags: "--lang=py", pos: "x"},
		{args: []string{"--lang", "-weird"}, flags: "--lang=-weird", pos: ""},
		{args: []string{"--", "--force", "-x"}, flags: "", pos: "--force,-x"},
		{args: []string{"-", "a"}, flags: "", pos: "-,a"},
		{args: []string{"--nope"}, err: "unknown option '--nope'"},
		{args: []string{"--nope=1"}, err: "unknown option '--nope'"},
		{args: []string{"-x"}, err: "unknown option '-x'"},
		{args: []string{"--lang"}, err: "option '--lang' needs a value"},
		{args: []string{"--force=1"}, err: "option '--force' takes no value"},
		{args: []string{"--force", "--force"}, err: "option '--force' given more than once"},
	} {
		flags, pos, err := splitFlags("cmd", c.args, spec)
		if c.err != "" {
			if err == nil || err.Error() != c.err {
				t.Errorf("splitFlags(%q) err = %v, want %q", c.args, err, c.err)
			}
			if _, ok := errors.AsType[*usageError](err); err != nil && !ok {
				t.Errorf("splitFlags(%q) err is %T, want *usageError", c.args, err)
			}
			continue
		}
		var got []string
		for k, v := range flags {
			got = append(got, k+"="+v)
		}
		if err != nil || strings.Join(got, ",") != c.flags || strings.Join(pos, ",") != c.pos {
			t.Errorf("splitFlags(%q) = %v %v %v, want flags %q pos %q", c.args, got, pos, err, c.flags, c.pos)
		}
	}
}

func TestPositiveInt(t *testing.T) {
	for in, want := range map[string]int{"1": 1, "25": 25, "007": 7} {
		if got, err := positiveInt("history", "history", in); err != nil || got != want {
			t.Errorf("positiveInt(%q) = %d, %v", in, got, err)
		}
	}
	for _, in := range []string{"0", "-1", "-n", "abc", "", "1.5", "99999999999999999999"} {
		_, err := positiveInt("history", "history", in)
		if err == nil || err.Error() != fmt.Sprintf("'history' expects a number, got '%s'", in) {
			t.Errorf("positiveInt(%q) err = %v", in, err)
		}
	}
}

func TestWantArgs(t *testing.T) {
	if err := wantArgs("x", []string{"a"}, 1, 2); err != nil {
		t.Error(err)
	}
	if err := wantArgs("x", nil, 1, 2); err == nil || err.Error() != "missing argument" {
		t.Errorf("too few: %v", err)
	}
	if err := wantArgs("x", []string{"a", "b", "c", "d"}, 1, 2); err == nil || err.Error() != "unexpected argument 'c'" {
		t.Errorf("too many: %v", err)
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

func TestRunSyncBeforeInit(t *testing.T) {
	r, _ := newTestRunner(t)
	for _, args := range [][]string{{"push"}, {"pull"}, {"pull", "--force"}, {"status"}} {
		err := runSync(r, args)
		if err == nil || !strings.Contains(err.Error(), "uc sync init") {
			t.Errorf("runSync(%q) error = %v, want 'run uc sync init' hint", args, err)
		}
	}
}

// The full alias flow through the CLI, including the strict --desc rule.
func TestAliasAddCLI(t *testing.T) {
	newHome(t)
	out, stderr, code := uc(t, "alias", "add", "gs", "--desc", "short status", "git", "status")
	if code != 0 || out != "added alias gs -> git status\n" || stderr != "" {
		t.Errorf("exit %d, out %q, stderr %q", code, out, stderr)
	}
	out, _, _ = uc(t, "alias", "list")
	if out != "gs  short status\n" {
		t.Errorf("alias list = %q", out)
	}

	_, stderr, code = uc(t, "alias", "add", "gs2", "git", "status", "--desc", "short status")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	want := "uc: note: '--desc' is part of the alias command. To set a description, put it before the command:\n" +
		"          uc alias add gs2 --desc \"short status\" git status\n"
	if stderr != want {
		t.Errorf("stderr = %q\nwant     %q", stderr, want)
	}
}

func TestHistoryClearCLI(t *testing.T) {
	newHome(t)
	out, _, code := uc(t, "history", "clear")
	if code != 0 || out != "history cleared\n" {
		t.Errorf("exit %d, out %q", code, out)
	}
}

func TestReport(t *testing.T) {
	var b bytes.Buffer
	if code := report(&b, nil); code != 0 || b.Len() != 0 {
		t.Errorf("nil: %d %q", code, b.String())
	}
	b.Reset()
	if code := report(&b, errors.New("boom")); code != 1 || b.String() != "uc: boom\n" {
		t.Errorf("plain: %d %q", code, b.String())
	}
	b.Reset()
	if code := report(&b, usagef("", "unknown option '--x'")); code != 2 || !strings.HasPrefix(b.String(), "uc: unknown option '--x'\nusage: uc <name> [<args>]") {
		t.Errorf("usage: %d %q", code, b.String())
	}
}

// commands.json problems are reported normally, but never during tab
// completion, where stderr lands in the user's prompt line.
func TestCompletionSilencesStoreWarnings(t *testing.T) {
	h := newHome(t)
	if err := os.MkdirAll(filepath.Join(h.configDir, "uc"), 0o700); err != nil {
		t.Fatal(err)
	}
	cj := `{"version": 1, "aliases": {"dup": {"command": "a"}, "../evil": {"command": "x"}}, "functions": {"dup": {"body": "b"}}}`
	if err := os.WriteFile(filepath.Join(h.configDir, "uc", "commands.json"), []byte(cj), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, stderr, _ := uc(t, "list"); !strings.Contains(stderr, "warning") {
		t.Errorf("list: want warnings, stderr %q", stderr)
	}
	if _, stderr, code := uc(t, "__complete", "d"); code != 0 || stderr != "" {
		t.Errorf("__complete: exit %d, stderr %q", code, stderr)
	}
}

// A script named after a built-in added later (export, import) can't run;
// uc says so instead of silently running the built-in, and it can still
// be removed.
func TestShadowedByBuiltinWarns(t *testing.T) {
	h := newHome(t)
	scripts := filepath.Join(h.ucHome, "scripts")
	if err := os.MkdirAll(scripts, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scripts, "import.sh"), []byte("#!/bin/sh\necho hi\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, stderr, _ := uc(t, "import", "nope.tar.gz")
	if !strings.Contains(stderr, "your script 'import' is hidden by the built-in 'uc import'") {
		t.Errorf("import: stderr %q", stderr)
	}
	stdout, _, code := uc(t, "list")
	if code != 0 || !strings.Contains(stdout, "hidden by the built-in 'uc import'") {
		t.Errorf("list: exit %d, stdout %q", code, stdout)
	}
	if _, stderr, code := uc(t, "remove", "import"); code != 0 {
		t.Errorf("remove import: exit %d, stderr %q", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(scripts, "import.sh")); !os.IsNotExist(err) {
		t.Errorf("import.sh still exists: %v", err)
	}
	if _, stderr, _ := uc(t, "list"); strings.Contains(stderr, "hidden") {
		t.Errorf("list after remove: stderr %q", stderr)
	}
}
