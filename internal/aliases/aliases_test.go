package aliases

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAdd_LoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aliases.json")
	desc := "Show git status"

	if err := Add(path, "gs", Alias{Command: "git status", Desc: &desc}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	a, ok := m["gs"]
	if !ok {
		t.Fatalf("alias %q not found", "gs")
	}
	if a.Command != "git status" {
		t.Errorf("got %+v", a)
	}
	if a.Desc == nil || *a.Desc != desc {
		t.Errorf("Desc = %v, want %q", a.Desc, desc)
	}
}

func TestRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aliases.json")
	if err := Add(path, "gs", Alias{Command: "git status"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Remove(path, "gs"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := m["gs"]; ok {
		t.Errorf("alias %q still present after Remove", "gs")
	}
}

func TestRemove_NotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aliases.json")
	if err := Remove(path, "nope"); err == nil {
		t.Fatal("expected error removing a nonexistent alias")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("got %+v, want empty", m)
	}
}

func TestNames_Sorted(t *testing.T) {
	m := map[string]Alias{"zeta": {}, "alpha": {}, "mid": {}}
	names := Names(m)
	want := []string{"alpha", "mid", "zeta"}
	for i, n := range names {
		if n != want[i] {
			t.Errorf("names[%d] = %q, want %q", i, n, want[i])
		}
	}
}

func TestAppendHazard(t *testing.T) {
	cases := []struct {
		command string
		want    string // substring, "" = no hazard
	}{
		// Clean commands: the args append cleanly at the end.
		{"git status", ""},
		{"ls -la | less", ""},
		{"echo hi && echo bye", ""},
		{"", ""},
		// Comments swallow the appended args.
		{"echo hi # my note", "comment"},
		{"# just a note", "comment"},
		{"echo hi #note", "comment"},
		// A # inside quotes is literal text, not a comment.
		{`echo "#not a comment"`, ""},
		{"echo '# also not one'", ""},
		{"grep '#' file", ""},
		// Trailing control operators detach the appended args.
		{"long-task &", "separate command"},
		{"setup;", "separate command"},
		{"a && b &&", "separate command"},
		{"a || b ||", "separate command"},
		{"cat file |", "command on the right"},
		{"  trailing-space &  ", "separate command"},
		// An operator character inside an unclosed quote is literal.
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
		if got == "" {
			t.Errorf("AppendHazard(%q) = \"\", want a hazard mentioning %q", c.command, c.want)
			continue
		}
		if !strings.Contains(got, c.want) {
			t.Errorf("AppendHazard(%q) = %q, want it to mention %q", c.command, got, c.want)
		}
	}
}
