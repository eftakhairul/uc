package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTestStore returns a Store in a temp dir with warnings captured.
func newTestStore(t *testing.T) (*Store, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	warn := &bytes.Buffer{}
	s := New(filepath.Join(dir, "commands.json"), filepath.Join(dir, "aliases.json"), filepath.Join(dir, "functions.json"))
	s.Warn = warn
	return s, warn
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// snapshot lists the dir's file names, to assert nothing was written.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.Name() + "\n")
	}
	return b.String()
}

const legacyAliases = `{
  "gs": {"command": "git status", "desc": "short status", "usage": null, "examples": null},
  "both": {"command": "echo alias", "desc": null, "usage": null, "examples": null}
}`

const legacyFunctions = `{
  "ship": {"body": "uc build && uc deploy", "desc": null, "usage": "ship", "examples": ["ship"]},
  "both": {"body": "echo function", "desc": null, "usage": null, "examples": null}
}`

func TestLoadEmpty(t *testing.T) {
	s, _ := newTestStore(t)
	f, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.Aliases == nil || f.Functions == nil || len(f.Aliases)+len(f.Functions) != 0 {
		t.Fatalf("want empty non-nil maps, got %+v", f)
	}
	if got := snapshot(t, filepath.Dir(s.Path)); got != "" {
		t.Errorf("Load wrote files: %q", got)
	}
}

func TestLoadLegacyAliasesOnly(t *testing.T) {
	s, _ := newTestStore(t)
	write(t, s.LegacyAliases, `{"gs": {"command": "git status", "desc": "short status", "usage": null, "examples": null}}`)
	f, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if a := f.Aliases["gs"]; a.Command != "git status" || a.Desc != "short status" {
		t.Errorf("alias gs = %+v", a)
	}
	if exists(s.Path) {
		t.Error("Load created commands.json")
	}
}

func TestLoadLegacyFunctionsOnly(t *testing.T) {
	s, _ := newTestStore(t)
	write(t, s.LegacyFunctions, `{"ship": {"body": "uc deploy", "desc": null, "usage": "ship", "examples": ["ship"]}}`)
	f, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if fn := f.Functions["ship"]; fn.Body != "uc deploy" || fn.Usage != "ship" || len(fn.Examples) != 1 {
		t.Errorf("function ship = %+v", fn)
	}
	if exists(s.Path) {
		t.Error("Load created commands.json")
	}
}

func TestLoadLegacyBoth(t *testing.T) {
	s, _ := newTestStore(t)
	write(t, s.LegacyAliases, legacyAliases)
	write(t, s.LegacyFunctions, legacyFunctions)
	before := snapshot(t, filepath.Dir(s.Path))
	f, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.Aliases["gs"].Command != "git status" || f.Functions["ship"].Body != "uc build && uc deploy" {
		t.Errorf("got %+v", f)
	}
	if after := snapshot(t, filepath.Dir(s.Path)); after != before {
		t.Errorf("Load changed the directory: %q -> %q", before, after)
	}
}

func TestLoadLegacyNameInBoth(t *testing.T) {
	s, warn := newTestStore(t)
	write(t, s.LegacyAliases, legacyAliases)
	write(t, s.LegacyFunctions, legacyFunctions)
	for range 3 {
		f, err := s.Load()
		if err != nil {
			t.Fatal(err)
		}
		if f.Kind("both") != "alias" || f.Aliases["both"].Command != "echo alias" {
			t.Fatalf("want the alias kept, got kind %q", f.Kind("both"))
		}
	}
	if n := strings.Count(warn.String(), "was both an alias and a function"); n != 1 {
		t.Errorf("warning printed %d times, want once:\n%s", n, warn)
	}
}

func TestSaveMigratesLegacy(t *testing.T) {
	s, warn := newTestStore(t)
	write(t, s.LegacyAliases, legacyAliases)
	write(t, s.LegacyFunctions, legacyFunctions)

	if err := s.SetAlias("new", Alias{Command: "echo new"}); err != nil {
		t.Fatal(err)
	}
	if exists(s.LegacyAliases) || exists(s.LegacyFunctions) {
		t.Error("legacy files still in place after the first write")
	}
	if !exists(s.LegacyAliases+".migrated") || !exists(s.LegacyFunctions+".migrated") {
		t.Error("legacy files not kept as *.migrated")
	}
	if !strings.Contains(warn.String(), "merged aliases.json and functions.json into commands.json") {
		t.Errorf("missing migration note: %q", warn)
	}

	f, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gs", "both", "new"} {
		if f.Kind(name) != "alias" {
			t.Errorf("%s: kind %q, want alias", name, f.Kind(name))
		}
	}
	if f.Kind("ship") != "function" {
		t.Errorf("ship: kind %q, want function", f.Kind("ship"))
	}
}

func TestSaveSecondTimeQuiet(t *testing.T) {
	s, warn := newTestStore(t)
	write(t, s.LegacyAliases, legacyAliases)
	if err := s.SetAlias("a", Alias{Command: "x"}); err != nil {
		t.Fatal(err)
	}
	warn.Reset()
	if err := s.SetAlias("b", Alias{Command: "y"}); err != nil {
		t.Fatal(err)
	}
	if warn.Len() != 0 {
		t.Errorf("second save printed %q", warn)
	}
}

func TestLoadPrefersCommandsJSON(t *testing.T) {
	s, _ := newTestStore(t)
	write(t, s.LegacyAliases, legacyAliases)
	write(t, s.Path, `{"version": 1, "aliases": {"only": {"command": "x"}}}`)
	f, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.Kind("gs") != "" || f.Kind("only") != "alias" {
		t.Errorf("legacy file was read although commands.json exists: %+v", f)
	}
	// And a later save leaves the stray legacy file alone.
	if err := s.SetAlias("b", Alias{Command: "y"}); err != nil {
		t.Fatal(err)
	}
	if !exists(s.LegacyAliases) {
		t.Error("save renamed a legacy file although commands.json already existed")
	}
}

func TestMigrate(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if exists(s.Path) {
		t.Error("Migrate wrote commands.json with nothing to migrate")
	}
	write(t, s.LegacyFunctions, legacyFunctions)
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if !exists(s.Path) || exists(s.LegacyFunctions) {
		t.Error("Migrate did not migrate")
	}
}

func TestSetAliasCollidesWithFunction(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.SetFunction("ship", Function{Body: "x"}); err != nil {
		t.Fatal(err)
	}
	err := s.SetAlias("ship", Alias{Command: "y"})
	if err == nil || err.Error() != "'ship' is already a function — remove it first with 'uc remove ship'" {
		t.Errorf("err = %v", err)
	}
}

func TestSetFunctionCollidesWithAlias(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.SetAlias("gs", Alias{Command: "x"}); err != nil {
		t.Fatal(err)
	}
	err := s.SetFunction("gs", Function{Body: "y"})
	if err == nil || err.Error() != "'gs' is already an alias — remove it first with 'uc remove gs'" {
		t.Errorf("err = %v", err)
	}
	// Replacing the same kind is how edit saves.
	if err := s.SetAlias("gs", Alias{Command: "z"}); err != nil {
		t.Errorf("replace same kind: %v", err)
	}
}

func TestRemoveKinds(t *testing.T) {
	s, _ := newTestStore(t)
	_ = s.SetAlias("gs", Alias{Command: "x"})
	_ = s.SetFunction("ship", Function{Body: "y"})

	if err := s.RemoveAlias("ship"); err == nil || err.Error() != "no alias named 'ship'" {
		t.Errorf("RemoveAlias(function) = %v", err)
	}
	if err := s.RemoveFunction("gs"); err == nil || err.Error() != "no function named 'gs'" {
		t.Errorf("RemoveFunction(alias) = %v", err)
	}
	if kind, err := s.Remove("ship"); err != nil || kind != "function" {
		t.Errorf("Remove(ship) = %q, %v", kind, err)
	}
	if err := s.RemoveAlias("gs"); err != nil {
		t.Errorf("RemoveAlias(gs) = %v", err)
	}
	if _, err := s.Remove("gs"); err == nil {
		t.Error("Remove of a missing name: want error")
	}
}

func TestLoadNewerVersion(t *testing.T) {
	s, _ := newTestStore(t)
	write(t, s.Path, `{"version": 2, "aliases": {}}`)
	_, err := s.Load()
	if err == nil || err.Error() != "commands.json was written by a newer uc (format 2) — upgrade uc" {
		t.Errorf("err = %v", err)
	}
}

func TestLoadCorrupt(t *testing.T) {
	s, _ := newTestStore(t)
	write(t, s.Path, "{\n  \"version\": 1,\n  \"aliases\": {,\n}")
	_, err := s.Load()
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "cannot read commands.json: invalid JSON at line 3") || strings.Contains(msg, "invalid character") {
		t.Errorf("err = %q", msg)
	}
}

func TestLoadDropsUnsafeNames(t *testing.T) {
	s, warn := newTestStore(t)
	write(t, s.Path, `{"version": 1, "aliases": {"../evil": {"command": "x"}, "ok": {"command": "y"}}, "functions": {".hidden": {"body": "z"}}}`)
	f, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.Kind("../evil") != "" || f.Kind(".hidden") != "" || f.Kind("ok") != "alias" {
		t.Errorf("got %+v", f)
	}
	if !strings.Contains(warn.String(), `ignoring alias "../evil"`) {
		t.Errorf("warning = %q", warn)
	}
}

func TestOmitEmpty(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.SetAlias("gs", Alias{Command: "git status"}); err != nil {
		t.Fatal(err)
	}
	got := read(t, s.Path)
	for _, bad := range []string{"null", `"desc"`, `"usage"`, `"examples"`} {
		if strings.Contains(got, bad) {
			t.Errorf("commands.json contains %s:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, `"version": 1`) {
		t.Errorf("commands.json missing the version:\n%s", got)
	}
}

func TestRoundTripMetadata(t *testing.T) {
	s, _ := newTestStore(t)
	want := Function{Body: "uc build && uc test", Desc: "pipeline", Usage: "ship", Examples: []string{"ship", "ship -v"}}
	if err := s.SetFunction("ship", want); err != nil {
		t.Fatal(err)
	}
	f, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	got := f.Functions["ship"]
	if got.Body != want.Body || got.Desc != want.Desc || got.Usage != want.Usage || strings.Join(got.Examples, "|") != "ship|ship -v" {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestNamesSorted(t *testing.T) {
	f := NewFile()
	for _, n := range []string{"zeta", "alpha", "mid"} {
		f.Aliases[n] = Alias{}
		f.Functions[n+"f"] = Function{}
	}
	if got := strings.Join(f.AliasNames(), ","); got != "alpha,mid,zeta" {
		t.Errorf("AliasNames = %s", got)
	}
	if got := strings.Join(f.FunctionNames(), ","); got != "alphaf,midf,zetaf" {
		t.Errorf("FunctionNames = %s", got)
	}
}

func TestUpdateErrorWritesNothing(t *testing.T) {
	s, _ := newTestStore(t)
	err := s.Update(func(f *File) error {
		f.Aliases["x"] = Alias{Command: "y"}
		return os.ErrInvalid
	})
	if err != os.ErrInvalid {
		t.Fatalf("err = %v", err)
	}
	if exists(s.Path) {
		t.Error("a failed Update wrote commands.json")
	}
}

func TestAppendHazard(t *testing.T) {
	cases := []struct {
		command string
		want    string // substring, "" = no hazard
	}{
		{"git status", ""},
		{"ls -la | less", ""},
		{"echo hi && echo bye", ""},
		{"", ""},
		{"echo hi # my note", "comment"},
		{"# just a note", "comment"},
		{"echo hi #note", "comment"},
		{`echo "#not a comment"`, ""},
		{"echo '# also not one'", ""},
		{"grep '#' file", ""},
		{"long-task &", "separate command"},
		{"setup;", "separate command"},
		{"a && b &&", "separate command"},
		{"a || b ||", "separate command"},
		{"cat file |", "command on the right"},
		{"  trailing-space &  ", "separate command"},
		{`echo "tom & jerry`, ""},
	}
	for _, c := range cases {
		got := AppendHazard(c.command)
		if c.want == "" {
			if got != "" {
				t.Errorf("AppendHazard(%q) = %q, want no hazard", c.command, got)
			}
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("AppendHazard(%q) = %q, want it to mention %q", c.command, got, c.want)
		}
	}
}

// commands.json is edited by hand: shell operators stay readable.
func TestEncodeKeepsShellText(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.SetFunction("ship", Function{Body: "uc build && uc test > out.log <in"}); err != nil {
		t.Fatal(err)
	}
	if got := read(t, s.Path); !strings.Contains(got, `"uc build && uc test > out.log <in"`) || !strings.HasSuffix(got, "}\n") {
		t.Errorf("commands.json = %s", got)
	}
}
