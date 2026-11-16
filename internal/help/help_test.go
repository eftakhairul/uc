package help

import (
	"strings"
	"testing"

	"github.com/eftakhairul/uc/internal/reserved"
)

const wantOverview = `usage: uc <name> [<args>]
       uc <command> [<args>]
       uc [-h | --help] [-v | --version]

Run 'uc' with no arguments to pick a script, alias, or function
interactively.

These are the uc commands:

create and change scripts
   add          Register a script from a local file or a URL
   new          Scaffold a new script and open it in $EDITOR
   edit         Open a script, alias, or function in $EDITOR
   remove       Unregister a script, alias, or function

find and inspect
   list         List registered scripts, aliases, and functions
   which        Show what a name resolves to, and its kind
   history      Show, re-run, or clear recent invocations

aliases and functions
   alias        Add, remove, list, or edit aliases to shell commands
   function     Add, remove, list, or edit inline shell functions

sync and setup
   sync         Sync scripts, aliases, and functions through a git remote
   export       Write scripts, aliases, and functions to an archive
   import       Add scripts, aliases, and functions from an archive
   completion   Print a shell completion script (bash, zsh, fish)
   version      Print the uc version

See 'uc help <command>' to read about a specific command, or
'uc help <name>' for a script, alias, or function you registered.
`

func TestOverviewGolden(t *testing.T) {
	if got := Overview(); got != wantOverview {
		t.Errorf("Overview() mismatch\n--- got ---\n%s\n--- want ---\n%s", got, wantOverview)
	}
}

func TestOverviewWidth(t *testing.T) {
	for i, line := range strings.Split(Overview(), "\n") {
		if n := len([]rune(line)); n > 80 {
			t.Errorf("line %d is %d columns: %q", i+1, n, line)
		}
	}
}

// The overview must not drift from the set of commands uc actually has.
func TestOverviewCoversReserved(t *testing.T) {
	count := map[string]int{}
	for _, g := range groups {
		for _, e := range g.commands {
			count[e.name]++
		}
	}
	skip := map[string]bool{"ls": true, "rm": true, "help": true}
	for _, name := range reserved.Names {
		if reserved.Hidden[name] || skip[name] {
			continue
		}
		if count[name] != 1 {
			t.Errorf("%q appears %d times in the overview, want exactly once", name, count[name])
		}
	}
	for name := range count {
		if !reserved.Is(name) {
			t.Errorf("overview lists %q, which is not a reserved command", name)
		}
	}
}

func TestGroupsHaveHelp(t *testing.T) {
	for _, g := range groups {
		for _, e := range g.commands {
			if _, ok := Static(e.name); !ok {
				t.Errorf("%q is in the overview but has no help entry", e.name)
			}
		}
	}
	for _, name := range reserved.Names {
		if reserved.Hidden[name] {
			continue
		}
		if _, ok := Static(name); !ok {
			t.Errorf("reserved command %q has no help entry", name)
		}
	}
}

func TestEntriesStartWithUsage(t *testing.T) {
	for name, text := range staticText {
		block, rest, ok := strings.Cut(text, "\n\n")
		if !ok || strings.TrimSpace(rest) == "" {
			t.Errorf("%s: want a usage block, a blank line, then prose", name)
			continue
		}
		for i, line := range strings.Split(block, "\n") {
			prefix := "   or: uc "
			if i == 0 {
				prefix = "usage: uc "
			}
			if !strings.HasPrefix(line, prefix) {
				t.Errorf("%s: usage line %d = %q, want prefix %q", name, i+1, line, prefix)
			}
		}
	}
}

func TestUsage(t *testing.T) {
	got, ok := Usage("alias")
	want := "usage: uc alias add <name> [--desc <text>] [--] <command>...\n" +
		"   or: uc alias remove <name>\n" +
		"   or: uc alias list\n" +
		"   or: uc alias edit <name>"
	if !ok || got != want {
		t.Errorf("Usage(alias) = %q, %v", got, ok)
	}
	rm, _ := Usage("rm")
	remove, _ := Usage("remove")
	if rm != remove || !strings.Contains(rm, "or: uc rm <name>") {
		t.Errorf("Usage(rm) = %q, want the remove block", rm)
	}
	if u, ok := Usage(""); !ok || !strings.HasPrefix(u, "usage: uc <name>") {
		t.Errorf("Usage(\"\") = %q", u)
	}
	if u, ok := Usage("nope"); ok || u != "" {
		t.Errorf("Usage(nope) = %q, %v", u, ok)
	}
}

func TestStaticShortAliases(t *testing.T) {
	ls, _ := Static("ls")
	list, _ := Static("list")
	if ls != list {
		t.Error("Static(ls) differs from Static(list)")
	}
}

func TestRenderMeta(t *testing.T) {
	got := RenderMeta("killport", "kills a port", "killport <port>", []string{"killport 80"})
	want := "killport — kills a port\n\nUsage:\n  killport <port>\n\nExamples:\n  killport 80\n"
	if got != want {
		t.Errorf("got %q", got)
	}
	if got := RenderMeta("x", "", "", nil); got != "x — no description available\n" {
		t.Errorf("got %q", got)
	}
}
