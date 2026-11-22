package help

import (
	"fmt"
	"strings"
)

// entry is one line of the `uc help` overview.
type entry struct{ name, summary string }

// group is a headed block of overview lines, git-style.
type group struct {
	heading  string
	commands []entry
}

// groups is the `uc help` overview. Every visible reserved command (minus
// the short aliases ls/rm and help itself) appears exactly once; a test
// keeps this in step with reserved.Names and staticText.
var groups = []group{
	{"create and change scripts", []entry{
		{"add", "Register a script from a local file or a URL"},
		{"new", "Scaffold a new script and open it in $EDITOR"},
		{"edit", "Open a script, alias, or function in $EDITOR"},
		{"remove", "Unregister a script, alias, or function"},
	}},
	{"find and inspect", []entry{
		{"list", "List registered scripts, aliases, and functions"},
		{"which", "Show what a name resolves to, and its kind"},
		{"history", "Show, re-run, or clear recent invocations"},
	}},
	{"aliases and functions", []entry{
		{"alias", "Add, remove, list, or edit aliases to shell commands"},
		{"function", "Add, remove, list, or edit inline shell functions"},
	}},
	{"sync and setup", []entry{
		{"sync", "Sync scripts, aliases, and functions through a git remote"},
		{"export", "Write scripts, aliases, and functions to an archive"},
		{"import", "Add scripts, aliases, and functions from an archive"},
		{"completion", "Print a shell completion script (bash, zsh, fish)"},
		{"version", "Print the uc version"},
	}},
}

const overviewUsage = `usage: uc <name> [<args>]
       uc <command> [<args>]
       uc [-h | --help] [-v | --version]`

const overviewIntro = `Run 'uc' with no arguments to list your scripts, aliases, and
functions (or this overview, while nothing is registered).

These are the uc commands:`

const overviewFooter = `See 'uc help <command>' to read about a specific command, or
'uc help <name>' for a script, alias, or function you registered.`

// Overview returns the text printed by `uc help`, `uc -h`, and bare `uc`
// when nothing is registered (bare `uc` otherwise prints `uc list`).
func Overview() string {
	width := 0
	for _, g := range groups {
		for _, e := range g.commands {
			width = max(width, len(e.name))
		}
	}

	var b strings.Builder
	b.WriteString(overviewUsage + "\n\n")
	b.WriteString(overviewIntro + "\n")
	for _, g := range groups {
		b.WriteString("\n" + g.heading + "\n")
		for _, e := range g.commands {
			fmt.Fprintf(&b, "   %-*s%s\n", width+3, e.name, e.summary)
		}
	}
	b.WriteString("\n" + overviewFooter + "\n")
	return b.String()
}

// Usage returns the leading "usage:" / "or:" lines of a command's help
// entry (no trailing newline), and whether the command has one. Usage("")
// returns the overview's usage block. Usage errors print this, so the
// usage shown on a mistake can never disagree with `uc help <command>`.
func Usage(cmd string) (string, bool) {
	if cmd == "" {
		return overviewUsage, true
	}
	text, ok := Static(cmd)
	if !ok {
		return "", false
	}
	block, _, _ := strings.Cut(text, "\n\n")
	return block, true
}
