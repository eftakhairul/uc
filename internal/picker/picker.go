// Package picker implements the interactive fuzzy-filter TUI launched by
// bare `uc` (architecture §9.2). Built on bubbletea, the well-established
// Go TUI library — a conscious exception to the "no runtime dependencies"
// goal for compile-time-only code (architecture §9.2 implementation note).
package picker

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"

	"github.com/eftakhairul/uc/internal/commands"
	"github.com/eftakhairul/uc/internal/config"
	"github.com/eftakhairul/uc/internal/history"
	"github.com/eftakhairul/uc/internal/ranker"
	"github.com/eftakhairul/uc/internal/registry"
	"github.com/eftakhairul/uc/internal/shellsplit"
)

type mode int

const (
	modeFilter mode = iota
	modeArgs
)

type item struct {
	name string
	kind string
	desc string
}

type model struct {
	all      []item
	filtered []item
	cursor   int
	input    string
	mode     mode
	argInput string
	argErr   string // shown under the args line, e.g. an unclosed quote
	selected *item

	quit bool
	err  error
}

var (
	selectedStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	descStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	promptStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Bold(true)
)

func newModel(items []item) model {
	m := model{all: items, filtered: items}
	return m
}

func (m model) Init() tea.Cmd { return nil }

// fuzzyMatch reports whether all characters of pattern appear in s, in
// order (subsequence match), case-insensitive.
func fuzzyMatch(s, pattern string) bool {
	if pattern == "" {
		return true
	}
	p := []rune(strings.ToLower(pattern))
	i := 0
	for _, c := range strings.ToLower(s) {
		if i < len(p) && p[i] == c {
			i++
		}
	}
	return i == len(p)
}

func (m *model) applyFilter() {
	if m.input == "" {
		m.filtered = m.all
	} else {
		m.filtered = m.filtered[:0]
		var out []item
		for _, it := range m.all {
			if fuzzyMatch(it.name, m.input) || fuzzyMatch(it.desc, m.input) {
				out = append(out, it)
			}
		}
		m.filtered = out
	}
	if m.cursor >= len(m.filtered) {
		m.cursor = len(m.filtered) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	if m.mode == modeArgs {
		return m.updateArgs(keyMsg)
	}
	return m.updateFilter(keyMsg)
}

func (m model) updateFilter(keyMsg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch keyMsg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.quit = true
		return m, tea.Quit
	case tea.KeyEnter:
		if len(m.filtered) == 0 {
			return m, nil
		}
		sel := m.filtered[m.cursor]
		m.selected = &sel
		m.mode = modeArgs
		m.argInput = sel.name + " "
		return m, nil
	case tea.KeyUp, tea.KeyCtrlP:
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case tea.KeyDown, tea.KeyCtrlN:
		if m.cursor < len(m.filtered)-1 {
			m.cursor++
		}
		return m, nil
	case tea.KeyBackspace:
		if len(m.input) > 0 {
			r := []rune(m.input)
			m.input = string(r[:len(r)-1])
			m.applyFilter()
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		// KeySpace already carries ' ' in Runes; appending another would
		// insert two spaces and break any filter containing one.
		m.input += string(keyMsg.Runes)
		m.applyFilter()
		return m, nil
	}
	return m, nil
}

func (m model) updateArgs(keyMsg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch keyMsg.Type {
	case tea.KeyEsc, tea.KeyCtrlC:
		m.quit = true
		return m, tea.Quit
	case tea.KeyEnter:
		if _, _, err := parseArgLine(m.argInput); err != nil {
			m.argErr = err.Error()
			return m, nil
		}
		return m, tea.Quit
	case tea.KeyBackspace:
		if len(m.argInput) > 0 {
			r := []rune(m.argInput)
			m.argInput = string(r[:len(r)-1])
		}
		m.argErr = ""
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.argInput += string(keyMsg.Runes)
		m.argErr = ""
		return m, nil
	}
	return m, nil
}

func (m model) View() string {
	if m.mode == modeArgs {
		hint := "(enter to run, esc to cancel)"
		if m.argErr != "" {
			hint = m.argErr + " — fix it, or esc to cancel"
		}
		return fmt.Sprintf("%s %s\n\n%s\n", promptStyle.Render("run:"), m.argInput, hint)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s%s\n\n", promptStyle.Render("> "), m.input)

	if len(m.filtered) == 0 {
		b.WriteString(descStyle.Render("no matches"))
		return b.String()
	}

	const maxVisible = 15
	start := 0
	if m.cursor >= maxVisible {
		start = m.cursor - maxVisible + 1
	}
	end := min(start+maxVisible, len(m.filtered))

	for i := start; i < end; i++ {
		it := m.filtered[i]
		line := fmt.Sprintf("%-8s %s", "["+it.kind+"]", it.name)
		if it.desc != "" {
			line += "  " + descStyle.Render(it.desc)
		}
		if i == m.cursor {
			fmt.Fprintf(&b, "%s\n", selectedStyle.Render("> "+line))
		} else {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}

	b.WriteString(descStyle.Render("\n(type to filter, ^n/^p or arrows to move, enter to select, esc to quit)\n"))
	return b.String()
}

// Run launches the interactive picker. On selection it logs the invocation
// to history and hands off to the executor exactly like any other
// execution path (architecture §9.2) — it does not return on a successful
// run since the executor replaces the process image.
func Run(cfg *config.Config, r *commands.Runner) error {
	regItems, err := r.Reg.List()
	if err != nil {
		return err
	}
	// Nothing to pick from: show what uc can do instead. Checked before
	// history is read, so a broken history file can't block it.
	if len(regItems) == 0 {
		return r.Help("")
	}
	if !haveTerminal() {
		return errNoTerminal
	}

	entries, err := history.Load(cfg.HistoryPath())
	if err != nil {
		return err
	}
	hour, day, week, older := cfg.RecencyWeights()
	weights := ranker.Weights{Hour: hour, Day: day, Week: week, Older: older}

	names := make([]string, len(regItems))
	byName := make(map[string]registry.Item, len(regItems))
	for i, it := range regItems {
		names[i] = it.Name
		byName[it.Name] = it
	}
	ranked := ranker.Rank(names, entries, time.Now(), weights)

	items := make([]item, len(ranked))
	for i, rk := range ranked {
		it := byName[rk.Name]
		items[i] = item{name: it.Name, kind: string(it.Kind), desc: it.Desc}
	}

	p := tea.NewProgram(newModel(items))
	finalModel, err := p.Run()
	if err != nil {
		return err
	}

	fm := finalModel.(model)
	if fm.quit || fm.selected == nil {
		return nil
	}

	name, args, err := parseArgLine(fm.argInput)
	if err != nil {
		return err
	}
	if name == "" {
		return nil
	}
	return r.Run(name, args)
}

var errNoTerminal = errors.New("the interactive picker needs a terminal — run 'uc list' to see what is registered, or 'uc <name>' to run something")

// haveTerminal reports whether the picker can run: stdin is a terminal,
// or — as bubbletea falls back to when stdin is piped — the controlling
// terminal can be opened. (A character-device check isn't enough:
// /dev/null is one too.)
func haveTerminal() bool {
	if term.IsTerminal(os.Stdin.Fd()) {
		return true
	}
	tty := "/dev/tty"
	if runtime.GOOS == "windows" {
		tty = "CONIN$"
	}
	f, err := os.Open(tty)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// parseArgLine splits a picker arg-input line ("name arg1 'arg two'") into
// the name and its arguments with shell quoting rules, so a quoted
// argument containing spaces stays one argument.
func parseArgLine(line string) (string, []string, error) {
	fields, err := shellsplit.Split(line)
	if err != nil {
		return "", nil, err
	}
	if len(fields) == 0 {
		return "", nil, nil
	}
	return fields[0], fields[1:], nil
}
