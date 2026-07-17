// Package commands implements the thin CLI handlers for uc's management
// subcommands, calling into registry/executor/history/aliases/functions/help
// as needed (architecture §3.4, §7).
package commands

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/eftakhairul/uc/internal/aliases"
	"github.com/eftakhairul/uc/internal/completion"
	"github.com/eftakhairul/uc/internal/config"
	"github.com/eftakhairul/uc/internal/executor"
	"github.com/eftakhairul/uc/internal/functions"
	"github.com/eftakhairul/uc/internal/help"
	"github.com/eftakhairul/uc/internal/history"
	"github.com/eftakhairul/uc/internal/ranker"
	"github.com/eftakhairul/uc/internal/registry"
	"github.com/eftakhairul/uc/internal/reserved"
)

// Version is uc's version string, printed by `uc version`. Overridden at
// build time via -ldflags "-X .../commands.Version=...".
var Version = "dev"

// Runner bundles the resolved config and registry that every command needs.
type Runner struct {
	Cfg *config.Config
	Reg *registry.Registry
	Out io.Writer
}

func New(cfg *config.Config) *Runner {
	reg := registry.New(cfg.ScriptsDir, cfg.AliasesPath(), cfg.FunctionsPath())
	return &Runner{Cfg: cfg, Reg: reg, Out: os.Stdout}
}

// Run resolves name across all three tiers (script, alias, function),
// logs the invocation to history, then hands off to the executor. History
// must be written before exec since process replacement means uc never
// regains control afterward (architecture §3.1, §8.1). Returns an error
// only if resolution or exec setup fails — on success this call never
// returns.
func (r *Runner) Run(name string, args []string) error {
	resolved, err := r.Reg.Resolve(name)
	if err != nil {
		return err
	}
	return r.execResolved(name, resolved, args)
}

// execResolved logs entry then dispatches by kind.
func (r *Runner) execResolved(name string, resolved *registry.Resolved, args []string) error {
	entry := history.Entry{
		Name: name, Args: args, Timestamp: time.Now().UTC(), Kind: string(resolved.Kind),
	}
	if err := history.Append(r.Cfg.HistoryPath(), entry, r.Cfg.HistorySize()); err != nil {
		fmt.Fprintf(os.Stderr, "uc: warning: failed to record history: %v\n", err)
	}

	switch resolved.Kind {
	case registry.KindScript:
		return executor.Run(resolved.Path, args)
	case registry.KindAlias:
		return executor.RunAlias(resolved.Body, name, args)
	case registry.KindFunction:
		return executor.RunFunction(resolved.Body, name, args)
	default:
		return fmt.Errorf("unknown resolution kind %q", resolved.Kind)
	}
}

// List implements `uc list` / `uc ls`.
func (r *Runner) List() error {
	items, err := r.Reg.List()
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintln(r.Out, "no scripts, aliases, or functions registered — add one with `uc add <path> [name]`")
		return nil
	}

	nameWidth, kindWidth := 0, 0
	for _, it := range items {
		nameWidth = max(nameWidth, len(it.Name))
		kindWidth = max(kindWidth, len(it.Kind))
	}
	for _, it := range items {
		if it.Desc != "" {
			fmt.Fprintf(r.Out, "%-*s  %-*s  %s\n", nameWidth, it.Name, kindWidth, it.Kind, it.Desc)
		} else {
			fmt.Fprintf(r.Out, "%-*s  %-*s\n", nameWidth, it.Name, kindWidth, it.Kind)
		}
	}
	return nil
}

// Add implements `uc add <path> [name]`.
func (r *Runner) Add(srcPath, name string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("%s: %w", srcPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%s is a directory, not a file", srcPath)
	}

	if name == "" {
		name = filepath.Base(srcPath)
	}
	// Reserved subcommands always win at dispatch, so a script registered
	// under one would be unrunnable — reject up front, same as alias/function
	// add. Checked on the bare name since that's what's typed to invoke it.
	if reserved.Is(registry.BareName(name)) {
		return fmt.Errorf("%q is a reserved subcommand name", registry.BareName(name))
	}

	if err := r.Cfg.EnsureDirs(); err != nil {
		return err
	}

	destPath := filepath.Join(r.Cfg.ScriptsDir, name)
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("%s: %w", srcPath, err)
	}
	defer src.Close()

	dst, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return fmt.Errorf("create %s: %w", destPath, err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("copy %s -> %s: %w", srcPath, destPath, err)
	}
	if err := dst.Chmod(0o755); err != nil {
		return fmt.Errorf("chmod %s: %w", destPath, err)
	}

	fmt.Fprintf(r.Out, "added %s -> %s\n", srcPath, destPath)
	return nil
}

// Remove implements `uc remove` / `uc rm` — resolves name across all three
// kinds and unregisters it (architecture §3.4).
func (r *Runner) Remove(name string) error {
	resolved, err := r.Reg.Resolve(name)
	if err != nil {
		return err
	}
	switch resolved.Kind {
	case registry.KindScript:
		if err := os.Remove(resolved.Path); err != nil {
			return fmt.Errorf("remove %s: %w", resolved.Path, err)
		}
		fmt.Fprintf(r.Out, "removed %s\n", resolved.Path)
		return nil
	case registry.KindAlias:
		return r.AliasRemove(name)
	case registry.KindFunction:
		return r.FunctionRemove(name)
	default:
		return fmt.Errorf("unknown resolution kind %q", resolved.Kind)
	}
}

// Which implements `uc which <name>` — reports the resolved kind alongside
// the resolution detail (architecture §3.2, §3.4).
func (r *Runner) Which(name string) error {
	resolved, err := r.Reg.Resolve(name)
	if err != nil {
		return err
	}
	switch resolved.Kind {
	case registry.KindScript:
		fmt.Fprintf(r.Out, "%s (script)\n", resolved.Path)
	case registry.KindAlias:
		fmt.Fprintf(r.Out, "%s (alias): %s\n", name, resolved.Body)
	case registry.KindFunction:
		fmt.Fprintf(r.Out, "%s (function): %s\n", name, resolved.Body)
	}
	return nil
}

// Edit implements `uc edit <name>`. Scripts: exec $EDITOR on the file
// (process replacement, does not return on success). Aliases/functions:
// delegate to the temp-file round-trip (architecture §3.4).
func (r *Runner) Edit(name string) error {
	resolved, err := r.Reg.Resolve(name)
	if err != nil {
		return err
	}
	switch resolved.Kind {
	case registry.KindFunction:
		return r.FunctionEdit(name)
	case registry.KindAlias:
		return r.AliasEdit(name)
	}

	editorCmd := r.Cfg.EditorCommand()
	argv := append(strings.Fields(editorCmd), resolved.Path)
	if len(argv) == 0 {
		return fmt.Errorf("empty editor command")
	}
	return executor.ExecReplace(argv)
}

// Completion implements `uc completion <bash|zsh>`.
func (r *Runner) Completion(shell string) error {
	script, err := completion.Script(shell)
	if err != nil {
		return err
	}
	fmt.Fprint(r.Out, script)
	return nil
}

// Complete implements the hidden `uc __complete <partial>`. With
// completion.enabled false in settings.json it prints nothing — the shell
// function still calls back in, but gets no candidates.
func (r *Runner) Complete(partial string) error {
	if !r.Cfg.CompletionEnabled() {
		return nil
	}
	entries, err := history.Load(r.Cfg.HistoryPath())
	if err != nil {
		return err
	}
	hour, day, week, older := r.Cfg.RecencyWeights()
	weights := ranker.Weights{Hour: hour, Day: day, Week: week, Older: older}

	names, err := completion.Candidates(r.Reg, entries, weights, partial)
	if err != nil {
		return err
	}
	for _, n := range names {
		fmt.Fprintln(r.Out, n)
	}
	return nil
}

// History implements `uc history [n]` — prints the last n entries
// (default/max: the configured history_size), most recent first, with
// relative timestamps and kind.
func (r *Runner) History(n int) error {
	entries, err := history.Load(r.Cfg.HistoryPath())
	if err != nil {
		return err
	}
	limit := r.Cfg.HistorySize()
	if n <= 0 || n > limit {
		n = limit
	}

	// entries is oldest-first; reverse for most-recent-first, capped to n.
	recent := entries
	if len(recent) > n {
		recent = recent[len(recent)-n:]
	}

	if len(recent) == 0 {
		fmt.Fprintln(r.Out, "no history yet")
		return nil
	}

	now := time.Now()
	for i := range slices.Backward(recent) {
		e := recent[i]
		idx := len(recent) - i
		args := strings.Join(e.Args, " ")
		if args != "" {
			args = " " + args
		}
		kind := e.Kind
		if kind == "" {
			kind = "script"
		}
		fmt.Fprintf(r.Out, "%2d  %s%s  (%s, %s)\n", idx, e.Name, args, kind, relativeTime(now, e.Timestamp))
	}
	return nil
}

// HistoryRun implements `uc history run <n>` — re-executes the nth most
// recent entry (1-indexed) by re-resolving its name fresh (so edits to an
// alias/function definition since the original run are picked up) with its
// original args. Does not return on success.
func (r *Runner) HistoryRun(n int) error {
	entries, err := history.Load(r.Cfg.HistoryPath())
	if err != nil {
		return err
	}
	if n < 1 || n > len(entries) {
		return fmt.Errorf("no history entry #%d", n)
	}
	// entries is oldest-first; #1 is the most recent, i.e. the last element.
	e := entries[len(entries)-n]
	return r.Run(e.Name, e.Args)
}

func relativeTime(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		return fmt.Sprintf("%dm ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		return fmt.Sprintf("%dh ago", h)
	case d < 48*time.Hour:
		return "yesterday"
	default:
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%dd ago", days)
	}
}

// AliasAdd implements `uc alias add <name> <command...> [--desc "..."]` —
// command is arbitrary shell text, the same idea as a bash alias (e.g.
// `alias gs='git status'`); it isn't required to be a registered uc script
// (architecture §3.2b).
func (r *Runner) AliasAdd(name, command, desc string) error {
	if reserved.Is(name) {
		return fmt.Errorf("%q is a reserved subcommand name", name)
	}
	a := aliases.Alias{Command: command}
	if desc != "" {
		a.Desc = &desc
	}
	if err := aliases.Add(r.Cfg.AliasesPath(), name, a); err != nil {
		return err
	}
	fmt.Fprintf(r.Out, "added alias %s -> %s\n", name, command)
	r.warnIfShadowed(name, registry.KindAlias)
	return nil
}

// warnIfShadowed prints a note if name won't actually resolve to the kind
// just registered — e.g. a new function shadowed by a same-named script,
// which always wins under the fixed script > alias > function precedence
// (architecture §3.2).
func (r *Runner) warnIfShadowed(name string, kind registry.Kind) {
	resolved, err := r.Reg.Resolve(name)
	if err == nil && resolved.Kind != kind {
		fmt.Fprintf(os.Stderr, "uc: note: %s is shadowed by a %s of the same name — `uc %s` will run the %s\n",
			name, resolved.Kind, name, resolved.Kind)
	}
}

// AliasEdit implements `uc alias edit <name>` — the same temp-file
// round-trip as FunctionEdit, editing the alias's command text.
// Desc/usage/examples are preserved as-is. The result is trimmed of
// surrounding whitespace: the executor appends `"$@"` to the command text
// (architecture §3.2b), and an editor's trailing newline would otherwise
// push that append onto its own line as a separate command.
func (r *Runner) AliasEdit(name string) error {
	m, err := aliases.Load(r.Cfg.AliasesPath())
	if err != nil {
		return err
	}
	a, ok := m[name]
	if !ok {
		return fmt.Errorf("no alias named %q", name)
	}

	edited, err := r.editViaTempFile("uc-alias-*.sh", a.Command)
	if err != nil {
		return err
	}
	a.Command = strings.TrimSpace(edited)
	if err := aliases.Add(r.Cfg.AliasesPath(), name, a); err != nil {
		return err
	}
	fmt.Fprintf(r.Out, "updated alias %s -> %s\n", name, a.Command)
	return nil
}

// AliasRemove implements `uc alias remove <name>`.
func (r *Runner) AliasRemove(name string) error {
	if err := aliases.Remove(r.Cfg.AliasesPath(), name); err != nil {
		return err
	}
	fmt.Fprintf(r.Out, "removed alias %s\n", name)
	return nil
}

// AliasList implements `uc alias list` — a filtered view of `uc list`.
func (r *Runner) AliasList() error {
	m, err := aliases.Load(r.Cfg.AliasesPath())
	if err != nil {
		return err
	}
	names := aliases.Names(m)
	if len(names) == 0 {
		fmt.Fprintln(r.Out, "no aliases registered — add one with `uc alias add <name> <command...>`")
		return nil
	}
	nameWidth := 0
	for _, n := range names {
		nameWidth = max(nameWidth, len(n))
	}
	for _, n := range names {
		desc := ""
		if a := m[n]; a.Desc != nil {
			desc = *a.Desc
		}
		if desc != "" {
			fmt.Fprintf(r.Out, "%-*s  %s\n", nameWidth, n, desc)
		} else {
			fmt.Fprintf(r.Out, "%-*s\n", nameWidth, n)
		}
	}
	return nil
}

// FunctionAdd implements `uc function add <name> <body> [--desc "..."]`.
func (r *Runner) FunctionAdd(name, body, desc string) error {
	if reserved.Is(name) {
		return fmt.Errorf("%q is a reserved subcommand name", name)
	}
	f := functions.Function{Body: body}
	if desc != "" {
		f.Desc = &desc
	}
	if err := functions.Add(r.Cfg.FunctionsPath(), name, f); err != nil {
		return err
	}
	fmt.Fprintf(r.Out, "added function %s\n", name)
	r.warnIfShadowed(name, registry.KindFunction)
	return nil
}

// FunctionRemove implements `uc function remove <name>`.
func (r *Runner) FunctionRemove(name string) error {
	if err := functions.Remove(r.Cfg.FunctionsPath(), name); err != nil {
		return err
	}
	fmt.Fprintf(r.Out, "removed function %s\n", name)
	return nil
}

// FunctionList implements `uc function list` — a filtered view of `uc list`.
func (r *Runner) FunctionList() error {
	m, err := functions.Load(r.Cfg.FunctionsPath())
	if err != nil {
		return err
	}
	names := functions.Names(m)
	if len(names) == 0 {
		fmt.Fprintln(r.Out, `no functions registered — add one with "uc function add <name> <body>"`)
		return nil
	}
	nameWidth := 0
	for _, n := range names {
		nameWidth = max(nameWidth, len(n))
	}
	for _, n := range names {
		desc := ""
		if f := m[n]; f.Desc != nil {
			desc = *f.Desc
		}
		if desc != "" {
			fmt.Fprintf(r.Out, "%-*s  %s\n", nameWidth, n, desc)
		} else {
			fmt.Fprintf(r.Out, "%-*s\n", nameWidth, n)
		}
	}
	return nil
}

// FunctionEdit implements `uc function edit <name>` — a temp-file
// round-trip of the body through $EDITOR (architecture §3.2c).
func (r *Runner) FunctionEdit(name string) error {
	m, err := functions.Load(r.Cfg.FunctionsPath())
	if err != nil {
		return err
	}
	f, ok := m[name]
	if !ok {
		return fmt.Errorf("no function named %q", name)
	}

	edited, err := r.editViaTempFile("uc-function-*.sh", f.Body)
	if err != nil {
		return err
	}
	f.Body = edited
	if err := functions.Add(r.Cfg.FunctionsPath(), name, f); err != nil {
		return err
	}
	fmt.Fprintf(r.Out, "updated function %s\n", name)
	return nil
}

// editViaTempFile writes initial to a temp file, opens $EDITOR on it, and
// on clean exit reads the edited content back (deleting the temp file).
// This can't use process replacement like other edit paths since uc needs
// to regain control afterward to persist the result; a non-zero editor
// exit aborts, and nothing should be saved (architecture §6, decision 17).
func (r *Runner) editViaTempFile(pattern, initial string) (string, error) {
	tmp, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(initial); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp file: %w", err)
	}

	argv := append(strings.Fields(r.Cfg.EditorCommand()), tmpPath)
	if len(argv) == 0 {
		return "", fmt.Errorf("empty editor command")
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("editor exited with an error, nothing saved: %w", err)
	}

	edited, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", fmt.Errorf("read edited content: %w", err)
	}
	return string(edited), nil
}

// Help implements `uc help` (no arg) and `uc help <subcommand|name>`
// (architecture §3.5). Resolution order mirrors the main dispatch order:
// reserved subcommands first, then Registry resolution.
func (r *Runner) Help(arg string) error {
	if arg == "" {
		fmt.Fprint(r.Out, helpText)
		return nil
	}

	if reserved.Is(arg) && !reserved.Hidden[arg] {
		if text, ok := help.Static(arg); ok {
			fmt.Fprint(r.Out, text)
			return nil
		}
	}

	resolved, err := r.Reg.Resolve(arg)
	if err != nil {
		fmt.Fprintf(r.Out, "No help available for %q\n", arg)
		return nil
	}
	fmt.Fprint(r.Out, help.RenderMeta(resolved.Name, resolved.Desc, resolved.Usage, resolved.Examples))
	return nil
}

const helpText = `uc - personal CLI script dispatcher

Usage:
  uc <name> [args...]     Run a registered script, alias, or function
  uc                      Launch the interactive picker

Management commands:
  uc list, uc ls          List registered scripts, aliases, and functions
  uc add <path> [name]    Register a script
  uc remove <name>, rm    Unregister a script, alias, or function
  uc which <name>         Print what a name resolves to, and its kind
  uc edit <name>          Open a script, alias, or function in $EDITOR
  uc completion <shell>   Print a completion script (bash|zsh)
  uc history [n]          Show the last n invocations (default: history_size)
  uc history run <n>      Re-run the nth history entry
  uc alias add <name> <command...> [--desc "..."]   Create an alias to a shell command
  uc alias remove <name>  Delete an alias
  uc alias list           List aliases only
  uc alias edit <name>    Edit an alias's command in $EDITOR
  uc function add <name> <body> [--desc "..."]   Create an inline function
  uc function remove <name>   Delete a function
  uc function list        List functions only
  uc function edit <name> Edit a function's body in $EDITOR
  uc help                 Show this help
  uc help <subcommand|name>   Help for a subcommand, script, alias, or function
  uc version               Print version
`

// PrintVersion implements `uc version`.
func (r *Runner) PrintVersion() error {
	fmt.Fprintln(r.Out, Version)
	return nil
}
