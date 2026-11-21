package registry

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/eftakhairul/uc/internal/store"
)

func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	return store.New(filepath.Join(dir, "commands.json"), filepath.Join(dir, "aliases.json"), filepath.Join(dir, "functions.json"))
}

func newTestRegistry(t *testing.T, scriptsDir string) *Registry {
	t.Helper()
	return New(scriptsDir, newTestStore(t))
}

func addAlias(t *testing.T, r *Registry, name, command string) {
	t.Helper()
	if err := r.Store.SetAlias(name, store.Alias{Command: command}); err != nil {
		t.Fatalf("SetAlias: %v", err)
	}
}

func addFunction(t *testing.T, r *Registry, name, body string) {
	t.Helper()
	if err := r.Store.SetFunction(name, store.Function{Body: body}); err != nil {
		t.Fatalf("SetFunction: %v", err)
	}
}

func TestResolve_ExtensionPriority(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "greet.py", "#!/usr/bin/env python3\nprint('hi')\n")

	r := newTestRegistry(t, dir)
	resolved, err := r.Resolve("greet")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Kind != KindScript {
		t.Errorf("Kind = %q, want %q", resolved.Kind, KindScript)
	}
	if resolved.Path != filepath.Join(dir, "greet.py") {
		t.Errorf("got %s, want greet.py", resolved.Path)
	}
}

func TestResolve_NoExtensionWinsFirst(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "killport", "#!/bin/sh\necho hi\n")
	writeScript(t, dir, "killport.sh", "#!/bin/sh\necho hi\n")

	r := newTestRegistry(t, dir)
	_, err := r.Resolve("killport")
	if _, ok := errors.AsType[*AmbiguousNameError](err); !ok {
		t.Fatalf("expected AmbiguousNameError, got %T: %v", err, err)
	}
}

func TestResolve_NotFound(t *testing.T) {
	dir := t.TempDir()
	r := newTestRegistry(t, dir)
	_, err := r.Resolve("nope")
	if _, ok := errors.AsType[*NotFoundError](err); !ok {
		t.Fatalf("expected NotFoundError, got %T: %v", err, err)
	}
}

func TestResolve_FallsThroughToAlias(t *testing.T) {
	dir := t.TempDir()

	r := newTestRegistry(t, dir)
	addAlias(t, r, "gs", "git status")

	resolved, err := r.Resolve("gs")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Kind != KindAlias {
		t.Errorf("Kind = %q, want %q", resolved.Kind, KindAlias)
	}
	if resolved.Body != "git status" {
		t.Errorf("Body = %q, want %q", resolved.Body, "git status")
	}
}

func TestResolve_AliasDoesNotRequireRegisteredScript(t *testing.T) {
	dir := t.TempDir()

	r := newTestRegistry(t, dir)
	addAlias(t, r, "gs", "git status")

	// No script named "git" or "gs" is registered under ScriptsDir; the
	// alias must still resolve, since its command is arbitrary shell text,
	// not a reference to a registered uc script (unlike the old design).
	if _, err := r.Resolve("gs"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

func TestResolve_FallsThroughToFunction(t *testing.T) {
	dir := t.TempDir()
	r := newTestRegistry(t, dir)
	addFunction(t, r, "shiplt", "uc build && uc test")

	resolved, err := r.Resolve("shiplt")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Kind != KindFunction {
		t.Errorf("Kind = %q, want %q", resolved.Kind, KindFunction)
	}
	if resolved.Body != "uc build && uc test" {
		t.Errorf("Body = %q", resolved.Body)
	}
}

func TestResolve_ScriptWinsOverAliasAndFunction(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "dp.sh", "#!/bin/bash\necho script wins\n")

	r := newTestRegistry(t, dir)
	addAlias(t, r, "dp", "echo alias")

	resolved, err := r.Resolve("dp")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Kind != KindScript {
		t.Errorf("Kind = %q, want %q (script precedence)", resolved.Kind, KindScript)
	}
}

func TestExtractMeta(t *testing.T) {
	dir := t.TempDir()
	path := writeScript(t, dir, "greet.sh", "#!/bin/bash\n# @desc: greets the user\n# @usage: greet <name>\n# @example: greet world\n# @example: greet --loud\necho hi\n")

	meta, err := ExtractMeta(path)
	if err != nil {
		t.Fatalf("ExtractMeta: %v", err)
	}
	if meta.Desc != "greets the user" {
		t.Errorf("Desc = %q", meta.Desc)
	}
	if meta.Usage != "greet <name>" {
		t.Errorf("Usage = %q", meta.Usage)
	}
	want := []string{"greet world", "greet --loud"}
	if len(meta.Examples) != len(want) {
		t.Fatalf("Examples = %v, want %v", meta.Examples, want)
	}
	for i, ex := range meta.Examples {
		if ex != want[i] {
			t.Errorf("Examples[%d] = %q, want %q", i, ex, want[i])
		}
	}
}

func TestExtractMeta_Absent(t *testing.T) {
	dir := t.TempDir()
	path := writeScript(t, dir, "plain.sh", "#!/bin/bash\necho hi\n")

	meta, err := ExtractMeta(path)
	if err != nil {
		t.Fatalf("ExtractMeta: %v", err)
	}
	if meta.Desc != "" || meta.Usage != "" || len(meta.Examples) != 0 {
		t.Errorf("got %+v, want zero value", meta)
	}
}

func TestList_DedupesShadowedNames(t *testing.T) {
	scriptsDir := t.TempDir()
	writeScript(t, scriptsDir, "deploy.sh", "#!/bin/bash\n# @desc: deploy script\n")

	// The store allows one kind per name, so the shadowed entry here is an
	// alias; script precedence must still list the name once.
	r := newTestRegistry(t, scriptsDir)
	addAlias(t, r, "deploy", "echo alias")

	items, err := r.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1 (shadowed alias/function deduped): %+v", len(items), items)
	}
	if items[0].Kind != KindScript {
		t.Errorf("Kind = %q, want %q (script precedence)", items[0].Kind, KindScript)
	}
}

func TestList_MergesAllThreeKinds(t *testing.T) {
	scriptsDir := t.TempDir()
	writeScript(t, scriptsDir, "zeta.sh", "#!/bin/bash\n# @desc: zeta script\n")
	writeScript(t, scriptsDir, "alpha.py", "#!/usr/bin/env python3\n# @desc: alpha script\n")

	r := newTestRegistry(t, scriptsDir)
	addAlias(t, r, "dp", "echo alias")
	addFunction(t, r, "shiplt", "uc build")

	items, err := r.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 4 {
		t.Fatalf("got %d items, want 4: %+v", len(items), items)
	}
	want := map[string]Kind{"alpha": KindScript, "dp": KindAlias, "shiplt": KindFunction, "zeta": KindScript}
	wantOrder := []string{"alpha", "dp", "shiplt", "zeta"}
	for i, it := range items {
		if it.Name != wantOrder[i] {
			t.Errorf("items[%d].Name = %q, want %q", i, it.Name, wantOrder[i])
		}
		if it.Kind != want[it.Name] {
			t.Errorf("items[%d].Kind = %q, want %q", i, it.Kind, want[it.Name])
		}
	}
}

// A line longer than bufio.Scanner's 64KB buffer (minified JS, a binary
// registered by mistake) used to fail the metadata scan, which in turn
// hid the script from List entirely.
func TestExtractMeta_OversizedLine(t *testing.T) {
	dir := t.TempDir()
	giant := strings.Repeat("x", 70*1024)

	path := writeScript(t, dir, "huge.js", giant+"\n")
	meta, err := ExtractMeta(path)
	if err != nil {
		t.Fatalf("ExtractMeta: %v", err)
	}
	if meta.Desc != "" {
		t.Errorf("Desc = %q, want empty", meta.Desc)
	}

	// Tags before the oversized line are still collected.
	tagged := writeScript(t, dir, "tagged.js", "// x\n# @desc: minified thing\n"+giant+"\n")
	meta, err = ExtractMeta(tagged)
	if err != nil {
		t.Fatalf("ExtractMeta (tagged): %v", err)
	}
	if meta.Desc != "minified thing" {
		t.Errorf("Desc = %q, want the tag before the giant line", meta.Desc)
	}
}

func TestList_IncludesScriptsWithUnreadableMeta(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "normal.sh", "#!/bin/sh\n# @desc: a normal one\necho hi\n")
	writeScript(t, dir, "huge.js", strings.Repeat("x", 70*1024)+"\n")

	reg := newTestRegistry(t, dir)
	items, err := reg.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	found := map[string]string{}
	for _, it := range items {
		found[it.Name] = it.Desc
	}
	if _, ok := found["huge"]; !ok {
		t.Errorf("List = %v, want it to include the oversized-line script", found)
	}
	if found["normal"] != "a normal one" {
		t.Errorf("normal desc = %q, want it intact", found["normal"])
	}
}

func TestList_IncludesUnreadableScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission semantics")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	dir := t.TempDir()
	path := writeScript(t, dir, "secret.sh", "#!/bin/sh\necho hi\n")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })

	reg := newTestRegistry(t, dir)
	items, err := reg.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, it := range items {
		if it.Name == "secret" {
			return
		}
	}
	t.Errorf("List = %v, want the unreadable script to still be listed", items)
}

// Names that would escape the scripts directory, or name a dotfile, must
// never resolve (improvement spec §4.1).
func TestResolveRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	scriptsDir := filepath.Join(root, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeScript(t, root, "victim", "#!/bin/sh\necho outside\n")
	writeScript(t, scriptsDir, ".hidden", "#!/bin/sh\n")
	r := newTestRegistry(t, scriptsDir)

	for _, name := range []string{"../victim", "../scripts/../victim", "a/b", `a\b`, "..", ".", "", ".hidden", "x\x00y"} {
		if res, err := r.Resolve(name); err == nil {
			t.Errorf("Resolve(%q) = %+v, want not found", name, res)
		} else if _, ok := errors.AsType[*NotFoundError](err); !ok {
			t.Errorf("Resolve(%q) err = %T %v, want NotFoundError", name, err, err)
		}
		if _, err := r.ResolveScript(name); err == nil {
			t.Errorf("ResolveScript(%q) resolved, want not found", name)
		}
	}
}

func TestListSkipsDotfilesDirsAndDanglingLinks(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "real.sh", "#!/bin/sh\n")
	writeScript(t, dir, ".DS_Store", "junk")
	writeScript(t, dir, ".x.tmp-123", "partial")
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "dangling")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(dir, "real.sh"), filepath.Join(dir, "linked")); err != nil {
			t.Fatal(err)
		}
	}
	items, err := newTestRegistry(t, dir).List()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range items {
		got = append(got, it.Name)
	}
	want := "real"
	if runtime.GOOS != "windows" {
		want = "linked,real"
	}
	if strings.Join(got, ",") != want {
		t.Errorf("List names = %v, want %s", got, want)
	}
}

func TestMetadataIsCleaned(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "esc.sh", "#!/bin/sh\n# @desc: \x1b[31mRED\x1b[0m\n# @usage: u\x07sage\n# @example: ex\x1bample\n")
	r := newTestRegistry(t, dir)
	if err := r.Store.SetAlias("al", store.Alias{Command: "echo \x1b[2J", Desc: "a\x1b]0;title\x07lias"}); err != nil {
		t.Fatal(err)
	}

	res, err := r.Resolve("esc")
	if err != nil {
		t.Fatal(err)
	}
	if res.Desc != "[31mRED[0m" || res.Usage != "usage" || res.Examples[0] != "example" {
		t.Errorf("script meta not cleaned: %+v", res)
	}
	res, err = r.Resolve("al")
	if err != nil {
		t.Fatal(err)
	}
	if res.Desc != "a]0;titlelias" {
		t.Errorf("alias desc = %q", res.Desc)
	}
	if res.Body != "echo \x1b[2J" {
		t.Errorf("the body is what runs and must not be altered: %q", res.Body)
	}
	items, _ := r.List()
	for _, it := range items {
		if strings.ContainsRune(it.Desc, 0x1b) {
			t.Errorf("List desc for %s still has an escape: %q", it.Name, it.Desc)
		}
	}
}

func TestNotFoundMessage(t *testing.T) {
	for _, c := range []struct {
		err  NotFoundError
		want string
	}{
		{NotFoundError{Name: "xyzzy"}, "'xyzzy' is not a uc command or a registered name — see 'uc list'"},
		{NotFoundError{Name: "kilport", Similar: []string{"killport"}}, "'kilport' is not a uc command or a registered name.\n\nThe most similar name is\n\tkillport"},
		{NotFoundError{Name: "ad", Similar: []string{"add", "alias"}}, "'ad' is not a uc command or a registered name.\n\nThe most similar names are\n\tadd\n\talias"},
	} {
		if got := c.err.Error(); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}

func TestAmbiguousMessageSaysHowToRecover(t *testing.T) {
	dir := t.TempDir()
	writeScript(t, dir, "foo.sh", "#!/bin/sh\n")
	writeScript(t, dir, "foo.py", "#!/usr/bin/env python3\n")
	_, err := newTestRegistry(t, dir).Resolve("foo")
	if err == nil || !strings.Contains(err.Error(), "— remove one, for example: uc remove foo.py") {
		t.Errorf("err = %v", err)
	}
	// The full file name resolves, which is what makes that advice work.
	if _, err := newTestRegistry(t, dir).Resolve("foo.py"); err != nil {
		t.Errorf("Resolve(foo.py) = %v", err)
	}
}
