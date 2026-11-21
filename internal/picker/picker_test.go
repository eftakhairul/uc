package picker

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eftakhairul/uc/internal/commands"
	"github.com/eftakhairul/uc/internal/config"
	"github.com/eftakhairul/uc/internal/help"
)

func TestFuzzyMatch(t *testing.T) {
	cases := []struct {
		s, pattern string
		want       bool
	}{
		{"killport", "", true},
		{"killport", "kp", true},
		{"killport", "KLP", true},
		{"killport", "pk", false},
		{"killport", "killports", false},
		{"déploy", "dép", true},
		{"déploy", "épy", true},
		{"deploy", "é", false},
	}
	for _, c := range cases {
		if got := fuzzyMatch(c.s, c.pattern); got != c.want {
			t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", c.s, c.pattern, got, c.want)
		}
	}
}

func TestParseArgLine(t *testing.T) {
	name, args, err := parseArgLine("deploy --env=prod  --verbose")
	if err != nil || name != "deploy" || len(args) != 2 || args[0] != "--env=prod" || args[1] != "--verbose" {
		t.Errorf("got %q %v %v", name, args, err)
	}
	name, args, err = parseArgLine("  ")
	if err != nil || name != "" || args != nil {
		t.Errorf("got %q %v %v, want empty", name, args, err)
	}
}

// improvement spec §4.12b: quoted arguments keep their spaces.
func TestParseArgLineQuotes(t *testing.T) {
	name, args, err := parseArgLine(`commit -m "two words" 'and more'`)
	if err != nil || name != "commit" || strings.Join(args, "|") != "-m|two words|and more" {
		t.Errorf("got %q %q %v", name, args, err)
	}
	if _, _, err := parseArgLine(`commit -m "unclosed`); err == nil {
		t.Error("unclosed quote: want an error")
	}
}

func key(r rune) tea.KeyMsg {
	if r == ' ' {
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func typeInto(m tea.Model, s string) tea.Model {
	for _, r := range s {
		m, _ = m.Update(key(r))
	}
	return m
}

// improvement spec §4.12a: space must insert exactly one space.
func TestSpaceInsertsOneSpace(t *testing.T) {
	m := typeInto(newModel([]item{{name: "a"}, {name: "b", desc: "has two words"}}), "two w")
	got := m.(model)
	if got.input != "two w" {
		t.Errorf("filter input = %q, want %q", got.input, "two w")
	}
	if len(got.filtered) != 1 || got.filtered[0].name != "b" {
		t.Errorf("filter with a space matched %v", got.filtered)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = typeInto(m, "x y")
	if got := m.(model).argInput; got != "b x y" {
		t.Errorf("args input = %q, want %q", got, "b x y")
	}
}

func TestArgsUnclosedQuoteStaysOpen(t *testing.T) {
	m := tea.Model(newModel([]item{{name: "a"}}))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = typeInto(m, `"oops`)
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Error("enter with an unclosed quote quit the picker")
	}
	if !strings.Contains(m.View(), "unclosed quote") {
		t.Errorf("view = %q, want the error shown", m.View())
	}
}

func newRunner(t *testing.T) (*config.Config, *commands.Runner, *bytes.Buffer) {
	t.Helper()
	ucHome := filepath.Join(t.TempDir(), "uc-home")
	cfg := &config.Config{UCHome: ucHome, ScriptsDir: filepath.Join(ucHome, "scripts"), ConfigDir: filepath.Join(t.TempDir(), "config")}
	r := commands.New(cfg)
	out := &bytes.Buffer{}
	r.Out = out
	return cfg, r, out
}

func TestRunEmptyRegistryPrintsHelp(t *testing.T) {
	cfg, r, out := newRunner(t)
	if err := Run(cfg, r); err != nil {
		t.Fatal(err)
	}
	if out.String() != help.Overview() {
		t.Errorf("output = %q, want the help overview", out.String())
	}
}

func TestRunEmptyRegistryIgnoresBrokenHistory(t *testing.T) {
	cfg, r, out := newRunner(t)
	if err := os.MkdirAll(cfg.UCHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.HistoryPath(), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Run(cfg, r); err != nil {
		t.Fatal(err)
	}
	if out.String() != help.Overview() {
		t.Error("broken history blocked the help overview")
	}
}

// Under `go test` stdin is not a terminal, so with something registered the
// picker must refuse with advice rather than a library error.
func TestRunWithoutTerminal(t *testing.T) {
	cfg, r, _ := newRunner(t)
	if err := r.AliasAdd("gs", "git status", ""); err != nil {
		t.Fatal(err)
	}
	if haveTerminal() {
		t.Skip("a terminal is available")
	}
	err := Run(cfg, r)
	if err == nil || err.Error() != "the interactive picker needs a terminal — run 'uc list' to see what is registered, or 'uc <name>' to run something" {
		t.Errorf("err = %v", err)
	}
}
