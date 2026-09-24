package completion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eftakhairul/uc/internal/aliases"
	"github.com/eftakhairul/uc/internal/history"
	"github.com/eftakhairul/uc/internal/ranker"
	"github.com/eftakhairul/uc/internal/registry"
)

func writeScript(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// newTestRegistry returns a registry over scriptsDir with alias/function
// stores in a fresh temp dir, so tests that only need scripts get empty
// stores for free.
func newTestRegistry(t *testing.T, scriptsDir string) *registry.Registry {
	t.Helper()
	dir := t.TempDir()
	return registry.New(scriptsDir, filepath.Join(dir, "aliases.json"), filepath.Join(dir, "functions.json"))
}

func TestScript_Fish(t *testing.T) {
	script, err := Script("fish")
	if err != nil {
		t.Fatalf("Script(fish): %v", err)
	}
	for _, want := range []string{"complete -c uc", "-k", "__complete --describe", "commandline -ct"} {
		if !strings.Contains(script, want) {
			t.Errorf("fish script missing %q:\n%s", want, script)
		}
	}
}

// Adding fish must not perturb the bash/zsh scripts: they call the flag-less
// __complete and their output is part of uc's existing contract.
func TestScript_BashZshUnchangedByFish(t *testing.T) {
	for shell, want := range map[string]string{"bash": bashScript, "zsh": zshScript} {
		got, err := Script(shell)
		if err != nil {
			t.Fatalf("Script(%s): %v", shell, err)
		}
		if got != want {
			t.Errorf("Script(%s) = %q, want %q", shell, got, want)
		}
		if strings.Contains(got, "--describe") {
			t.Errorf("Script(%s) must not use --describe", shell)
		}
	}
}

func TestScript_UnsupportedShellMentionsFish(t *testing.T) {
	_, err := Script("nope")
	if err == nil {
		t.Fatal("expected an error for an unsupported shell")
	}
	if !strings.Contains(err.Error(), "fish") {
		t.Errorf("error %q does not mention fish", err)
	}
}

func TestDescribedCandidates_CarriesDescriptions(t *testing.T) {
	scripts := t.TempDir()
	writeScript(t, scripts, "killport", "#!/bin/sh\n# @desc: kills a process by port\necho hi\n")
	writeScript(t, scripts, "kbare", "#!/bin/sh\necho hi\n")

	reg := newTestRegistry(t, scripts)
	desc := "git status"
	if err := aliases.Add(reg.AliasesPath, "kgs", aliases.Alias{Command: "git status", Desc: &desc}); err != nil {
		t.Fatalf("alias add: %v", err)
	}

	pairs, err := DescribedCandidates(reg, nil, ranker.DefaultWeights, "k")
	if err != nil {
		t.Fatalf("DescribedCandidates: %v", err)
	}

	got := make(map[string]string, len(pairs))
	for _, p := range pairs {
		got[p.Name] = p.Desc
	}
	want := map[string]string{
		"kbare":    "",
		"kgs":      "git status",
		"killport": "kills a process by port",
	}
	for name, desc := range want {
		d, ok := got[name]
		if !ok {
			t.Errorf("candidate %q missing from %v", name, got)
			continue
		}
		if d != desc {
			t.Errorf("%q desc = %q, want %q", name, d, desc)
		}
	}
}

// Reserved subcommands complete, but carry no description: their help text is
// multi-line prose, not a one-liner fit for the pager.
func TestDescribedCandidates_ReservedNamesHaveNoDesc(t *testing.T) {
	pairs, err := DescribedCandidates(newTestRegistry(t, t.TempDir()), nil, ranker.DefaultWeights, "lis")
	if err != nil {
		t.Fatalf("DescribedCandidates: %v", err)
	}
	found := false
	for _, p := range pairs {
		if p.Name != "list" {
			continue
		}
		found = true
		if p.Desc != "" {
			t.Errorf("reserved name list has desc %q, want empty", p.Desc)
		}
	}
	if !found {
		t.Errorf("got %v, want the reserved name list among the candidates", pairs)
	}
}

func TestDescribedCandidates_PrefixFilters(t *testing.T) {
	scripts := t.TempDir()
	writeScript(t, scripts, "killport", "#!/bin/sh\necho hi\n")
	writeScript(t, scripts, "deploy", "#!/bin/sh\necho hi\n")

	pairs, err := DescribedCandidates(newTestRegistry(t, scripts), nil, ranker.DefaultWeights, "kill")
	if err != nil {
		t.Fatalf("DescribedCandidates: %v", err)
	}
	if len(pairs) != 1 || pairs[0].Name != "killport" {
		t.Errorf("got %v, want just killport", pairs)
	}
}

func TestDescribedCandidates_FrecencyOrder(t *testing.T) {
	scripts := t.TempDir()
	writeScript(t, scripts, "zebra", "#!/bin/sh\necho hi\n")
	writeScript(t, scripts, "apple", "#!/bin/sh\necho hi\n")

	// zebra sorts last alphabetically but was run recently, so frecency must
	// put it first — the ordering the fish script's -k flag preserves.
	entries := []history.Entry{
		{Name: "zebra", Timestamp: time.Now().Add(-time.Minute), Kind: "script"},
	}

	pairs, err := DescribedCandidates(newTestRegistry(t, scripts), entries, ranker.DefaultWeights, "")
	if err != nil {
		t.Fatalf("DescribedCandidates: %v", err)
	}
	if len(pairs) < 2 || pairs[0].Name != "zebra" {
		t.Fatalf("got %v, want zebra first", pairs)
	}
}

func TestCandidates_StripsDescriptions(t *testing.T) {
	scripts := t.TempDir()
	writeScript(t, scripts, "killport", "#!/bin/sh\n# @desc: kills a process by port\necho hi\n")

	names, err := Candidates(newTestRegistry(t, scripts), nil, ranker.DefaultWeights, "killport")
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(names) != 1 || names[0] != "killport" {
		t.Errorf("got %v, want [killport]", names)
	}
}
