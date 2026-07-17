package registry

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/eftakhairul/uc/internal/aliases"
	"github.com/eftakhairul/uc/internal/functions"
)

func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func newTestRegistry(t *testing.T, scriptsDir string) *Registry {
	t.Helper()
	dir := t.TempDir()
	return New(scriptsDir, filepath.Join(dir, "aliases.json"), filepath.Join(dir, "functions.json"))
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
	if _, ok := err.(*NotFoundError); !ok {
		t.Fatalf("expected NotFoundError, got %T: %v", err, err)
	}
}

func TestResolve_FallsThroughToAlias(t *testing.T) {
	dir := t.TempDir()

	aliasesPath := filepath.Join(dir, "aliases.json")
	if err := aliases.Add(aliasesPath, "gs", aliases.Alias{Command: "git status"}); err != nil {
		t.Fatalf("aliases.Add: %v", err)
	}

	r := New(dir, aliasesPath, filepath.Join(dir, "functions.json"))
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

	aliasesPath := filepath.Join(dir, "aliases.json")
	if err := aliases.Add(aliasesPath, "gs", aliases.Alias{Command: "git status"}); err != nil {
		t.Fatalf("aliases.Add: %v", err)
	}

	// No script named "git" or "gs" is registered under ScriptsDir; the
	// alias must still resolve, since its command is arbitrary shell text,
	// not a reference to a registered uc script (unlike the old design).
	r := New(dir, aliasesPath, filepath.Join(dir, "functions.json"))
	if _, err := r.Resolve("gs"); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
}

func TestResolve_FallsThroughToFunction(t *testing.T) {
	dir := t.TempDir()
	functionsPath := filepath.Join(dir, "functions.json")
	if err := functions.Add(functionsPath, "shiplt", functions.Function{Body: "uc build && uc test"}); err != nil {
		t.Fatalf("functions.Add: %v", err)
	}

	r := New(dir, filepath.Join(dir, "aliases.json"), functionsPath)
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

	aliasesPath := filepath.Join(dir, "aliases.json")
	if err := aliases.Add(aliasesPath, "dp", aliases.Alias{Command: "echo alias"}); err != nil {
		t.Fatalf("aliases.Add: %v", err)
	}

	r := New(dir, aliasesPath, filepath.Join(dir, "functions.json"))
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

	configDir := t.TempDir()
	aliasesPath := filepath.Join(configDir, "aliases.json")
	if err := aliases.Add(aliasesPath, "deploy", aliases.Alias{Command: "echo alias"}); err != nil {
		t.Fatalf("aliases.Add: %v", err)
	}
	functionsPath := filepath.Join(configDir, "functions.json")
	if err := functions.Add(functionsPath, "deploy", functions.Function{Body: "echo hi"}); err != nil {
		t.Fatalf("functions.Add: %v", err)
	}

	r := New(scriptsDir, aliasesPath, functionsPath)
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

	configDir := t.TempDir()
	aliasesPath := filepath.Join(configDir, "aliases.json")
	if err := aliases.Add(aliasesPath, "dp", aliases.Alias{Command: "echo alias"}); err != nil {
		t.Fatalf("aliases.Add: %v", err)
	}
	functionsPath := filepath.Join(configDir, "functions.json")
	if err := functions.Add(functionsPath, "shiplt", functions.Function{Body: "uc build"}); err != nil {
		t.Fatalf("functions.Add: %v", err)
	}

	r := New(scriptsDir, aliasesPath, functionsPath)
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
