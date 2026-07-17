// Package picker implements the interactive fuzzy-filter TUI launched by
// bare `uc` (architecture §9.2). Built on bubbletea, the well-established
// Go TUI library — a conscious exception to the "no runtime dependencies"
// goal for compile-time-only code (architecture §9.2 implementation note).
package picker

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/eftakhairul/uc/internal/commands"
	"github.com/eftakhairul/uc/internal/config"
	"github.com/eftakhairul/uc/internal/history"
	"github.com/eftakhairul/uc/internal/ranker"
	"github.com/eftakhairul/uc/internal/registry"
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
		m.input += string(keyMsg.Runes)
		if keyMsg.Type == tea.KeySpace {
			m.input += " "
		}
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
		return m, tea.Quit
	case tea.KeyBackspace:
		if len(m.argInput) > 0 {
			r := []rune(m.argInput)
			m.argInput = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes, tea.KeySpace:
		m.argInput += string(keyMsg.Runes)
		if keyMsg.Type == tea.KeySpace {
			m.argInput += " "
		}
		return m, nil
	}
	return m, nil
}

func (m model) View() string {
	if m.mode == modeArgs {
		return fmt.Sprintf("%s %s\n\n(enter to run, esc to cancel)\n", promptStyle.Render("run:"), m.argInput)
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

	if len(items) == 0 {
		fmt.Println("no scripts, aliases, or functions registered — add one with `uc add <path> [name]`")
		return nil
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

	name, args := parseArgLine(fm.argInput)
	return r.Run(name, args)
}

// parseArgLine splits a picker arg-input line ("name arg1 arg2") into the
// script name and its arguments, using simple whitespace splitting.
func parseArgLine(line string) (string, []string) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], fields[1:]
}
