package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// populate gives r two scripts, an alias, and a function.
func populate(t *testing.T, r *Runner) {
	t.Helper()
	if err := r.Cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	writeScript(t, r.Cfg.ScriptsDir, "killport", "#!/bin/sh\n# @desc: kills a port\necho kill $1\n")
	writeScript(t, r.Cfg.ScriptsDir, "hello.py", "print('hi')\n")
	if err := r.AliasAdd("gs", "git status", "short status"); err != nil {
		t.Fatal(err)
	}
	if err := r.FunctionAdd("ship", "uc build && uc deploy", ""); err != nil {
		t.Fatal(err)
	}
}

func listing(t *testing.T, r *Runner) string {
	t.Helper()
	var buf bytes.Buffer
	orig := r.Out
	r.Out = &buf
	defer func() { r.Out = orig }()
	if err := r.List(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestExportImportRoundTrip(t *testing.T) {
	a, aOut := newTestRunner(t)
	populate(t, a)
	archive := filepath.Join(t.TempDir(), "uc.tar.gz")
	aOut.Reset()
	if err := a.Export(archive, false); err != nil {
		t.Fatal(err)
	}
	if got := aOut.String(); got != "exported 2 scripts, 1 alias, 1 function to "+archive+"\n" {
		t.Errorf("export output = %q", got)
	}
	if !strings.Contains(errBuf(a).String(), "may contain secrets") {
		t.Errorf("missing secrets note: %q", errBuf(a).String())
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(archive); info.Mode().Perm() != 0o600 {
			t.Errorf("archive mode = %o, want 600", info.Mode().Perm())
		}
	}

	b, bOut := newTestRunner(t)
	if err := b.Import(archive, false, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"+ script hello.py", "+ script killport", "+ alias gs", "+ function ship",
		"imported 2 scripts, 1 alias, 1 function", "review scripts before running them"} {
		if !strings.Contains(bOut.String(), want) {
			t.Errorf("import output missing %q:\n%s", want, bOut.String())
		}
	}
	if listing(t, a) != listing(t, b) {
		t.Errorf("listings differ:\n%s\n---\n%s", listing(t, a), listing(t, b))
	}

	// A second import finds nothing new.
	bOut.Reset()
	if err := b.Import(archive, false, false); err != nil {
		t.Fatal(err)
	}
	if bOut.String() != "nothing to import — everything is already here\n" {
		t.Errorf("second import = %q", bOut.String())
	}
}

func TestExportStdoutImportStdin(t *testing.T) {
	a, aOut := newTestRunner(t)
	populate(t, a)
	aOut.Reset()
	if err := a.Export("-", false); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(aOut.Bytes(), []byte{0x1f, 0x8b}) {
		t.Fatal("stdout export is not gzip data (or has extra text)")
	}
	b, _ := newTestRunner(t)
	b.In = bytes.NewReader(aOut.Bytes())
	if err := b.Import("-", false, false); err != nil {
		t.Fatal(err)
	}
	if listing(t, a) != listing(t, b) {
		t.Error("listings differ after a stdin import")
	}
}

func snapshotDir(t *testing.T, r *Runner) string {
	t.Helper()
	var b strings.Builder
	entries, _ := os.ReadDir(r.Cfg.ScriptsDir)
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(r.Cfg.ScriptsDir, e.Name()))
		b.WriteString(e.Name() + "=" + string(data) + "\n")
	}
	data, _ := os.ReadFile(r.Cfg.CommandsPath())
	b.Write(data)
	return b.String()
}

func TestImportConflictWritesNothing(t *testing.T) {
	a, _ := newTestRunner(t)
	populate(t, a)
	writeScript(t, a.Cfg.ScriptsDir, "brand-new", "#!/bin/sh\n")
	archive := filepath.Join(t.TempDir(), "uc.tar.gz")
	if err := a.Export(archive, false); err != nil {
		t.Fatal(err)
	}

	b, _ := newTestRunner(t)
	if err := b.Cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	writeScript(t, b.Cfg.ScriptsDir, "killport", "#!/bin/sh\necho mine\n")
	if err := b.AliasAdd("gs", "git status -s", ""); err != nil {
		t.Fatal(err)
	}
	before := snapshotDir(t, b)

	err := b.Import(archive, false, false)
	want := "2 entries already exist with different content:\n  ~ script killport\n  ~ alias gs\npass --force to replace them, or --dry-run to preview"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v\nwant %s", err, want)
	}
	if after := snapshotDir(t, b); after != before {
		t.Errorf("a refused import changed files:\n%s\n---\n%s", before, after)
	}

	// --dry-run lists everything and writes nothing.
	var dry bytes.Buffer
	b.Out = &dry
	if err := b.Import(archive, false, true); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"+ script brand-new", "~ script killport", "~ alias gs", "+ function ship", "3 to add, 2 to replace — nothing written (--dry-run)"} {
		if !strings.Contains(dry.String(), line) {
			t.Errorf("dry run missing %q:\n%s", line, dry.String())
		}
	}
	if after := snapshotDir(t, b); after != before {
		t.Error("--dry-run wrote files")
	}

	// --force: the archive wins.
	if err := b.Import(archive, true, false); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(b.Cfg.ScriptsDir, "killport")); !strings.Contains(string(data), "echo kill") {
		t.Errorf("killport = %q", data)
	}
	f, _ := b.Reg.Store.Load()
	if f.Aliases["gs"].Command != "git status" {
		t.Errorf("gs = %q", f.Aliases["gs"].Command)
	}
}

func TestImportOneConflictWording(t *testing.T) {
	a, _ := newTestRunner(t)
	if err := a.AliasAdd("gs", "git status", ""); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "uc.tar.gz")
	if err := a.Export(archive, false); err != nil {
		t.Fatal(err)
	}
	b, _ := newTestRunner(t)
	if err := b.AliasAdd("gs", "git status -s", ""); err != nil {
		t.Fatal(err)
	}
	if err := b.Import(archive, false, false); err == nil || !strings.HasPrefix(err.Error(), "1 entry already exists with different content:") {
		t.Errorf("err = %v", err)
	}
}

func TestExportRefusesOverwrite(t *testing.T) {
	r, _ := newTestRunner(t)
	populate(t, r)
	archive := filepath.Join(t.TempDir(), "uc.tar.gz")
	if err := os.WriteFile(archive, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := r.Export(archive, false)
	if err == nil || err.Error() != "'"+archive+"' already exists — pass --force to overwrite" {
		t.Errorf("err = %v", err)
	}
	if data, _ := os.ReadFile(archive); string(data) != "precious" {
		t.Error("existing file overwritten")
	}
	if err := r.Export(archive, true); err != nil {
		t.Fatalf("--force: %v", err)
	}
}

func TestExportEmpty(t *testing.T) {
	r, _ := newTestRunner(t)
	if err := r.Export(filepath.Join(t.TempDir(), "x.tar.gz"), false); err == nil || err.Error() != "nothing to export" {
		t.Errorf("err = %v", err)
	}
}

func TestExportDefaultName(t *testing.T) {
	name := defaultExportName(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC))
	if !strings.HasPrefix(name, "uc-export-") || !strings.HasSuffix(name, "-20261116.tar.gz") || strings.ContainsAny(name, "/ ") {
		t.Errorf("name = %q", name)
	}

	r, _ := newTestRunner(t)
	populate(t, r)
	dir := t.TempDir()
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := r.Export("", false); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "uc-export-*.tar.gz"))
	if len(matches) != 1 {
		t.Errorf("default export file not found: %v", matches)
	}
}

func TestImportMissingFile(t *testing.T) {
	r, _ := newTestRunner(t)
	err := r.Import("/no/such.tar.gz", false, false)
	if err == nil || err.Error() != "cannot import '/no/such.tar.gz': no such file" {
		t.Errorf("err = %v", err)
	}
}

func TestImportRejectsBadArchive(t *testing.T) {
	r, _ := newTestRunner(t)
	path := filepath.Join(t.TempDir(), "bad.tar.gz")
	if err := os.WriteFile(path, []byte("not an archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.Import(path, false, false); err == nil || err.Error() != "cannot import: not a uc export file" {
		t.Errorf("err = %v", err)
	}
}

func TestImportedScriptsArePrivateAndExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	a, _ := newTestRunner(t)
	populate(t, a)
	archive := filepath.Join(t.TempDir(), "uc.tar.gz")
	if err := a.Export(archive, false); err != nil {
		t.Fatal(err)
	}
	b, _ := newTestRunner(t)
	if err := b.Import(archive, false, false); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(b.Cfg.ScriptsDir, "killport"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Errorf("mode = %o, want 700", info.Mode().Perm())
	}
}
