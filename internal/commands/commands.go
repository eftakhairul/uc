// Package commands implements the thin CLI handlers for uc's management
// subcommands, calling into registry/executor/history/store/help as needed
// (architecture §3.4, §7).
package commands

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/eftakhairul/uc/internal/completion"
	"github.com/eftakhairul/uc/internal/config"
	"github.com/eftakhairul/uc/internal/executor"
	"github.com/eftakhairul/uc/internal/gitsync"
	"github.com/eftakhairul/uc/internal/help"
	"github.com/eftakhairul/uc/internal/history"
	"github.com/eftakhairul/uc/internal/names"
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
	Out io.Writer // normal output
	Err io.Writer // warnings and notes; os.Stderr when nil
	In  io.Reader // stdin, for `uc import -`; os.Stdin when nil
}

func New(cfg *config.Config) *Runner {
	return &Runner{
		Cfg: cfg,
		Reg: registry.New(cfg.ScriptsDir, cfg.Store()),
		Out: os.Stdout,
		Err: os.Stderr,
		In:  os.Stdin,
	}
}

func (r *Runner) errOut() io.Writer {
	if r.Err != nil {
		return r.Err
	}
	return os.Stderr
}

func (r *Runner) in() io.Reader {
	if r.In != nil {
		return r.In
	}
	return os.Stdin
}

// notef prints a "uc: note:" line to the warning stream.
func (r *Runner) notef(format string, a ...any) {
	fmt.Fprintf(r.errOut(), "uc: note: "+format+"\n", a...)
}

// resolve resolves name, filling in near-miss suggestions when nothing
// matches.
func (r *Runner) resolve(name string) (*registry.Resolved, error) {
	res, err := r.Reg.Resolve(name)
	if err != nil {
		return nil, r.withSuggestions(err)
	}
	return res, nil
}

// WarnShadowed warns when the built-in command name also matches a user's
// script, alias, or function — which can never run, since built-ins win.
// This happens when an upgrade reserves a name the user already had (as
// `export` and `import` were).
func (r *Runner) WarnShadowed(name string) {
	if !reserved.Is(name) || reserved.Hidden[name] {
		return
	}
	res, err := r.Reg.Resolve(name)
	if err != nil {
		return
	}
	fmt.Fprintf(r.Err, "uc: warning: your %s '%s' is hidden by the built-in 'uc %s' and can't be run — re-add it under another name, then 'uc remove %s'\n",
		res.Kind, name, name, name)
}

// withSuggestions adds similar names to a NotFoundError.
func (r *Runner) withSuggestions(err error) error {
	if nf, ok := errors.AsType[*registry.NotFoundError](err); ok && nf.Similar == nil {
		nf.Similar = r.suggest(nf.Name)
	}
	return err
}

// Run resolves name across all three tiers (script, alias, function),
// logs the invocation to history, then hands off to the executor. History
// is written after the run is prepared — so a run that could never start
// isn't recorded — and before exec, since process replacement means uc
// never regains control afterward (architecture §3.1, §8.1). Returns an
// error only if resolution or exec setup fails; on success this call never
// returns.
func (r *Runner) Run(name string, args []string) error {
	resolved, err := r.resolve(name)
	if err != nil {
		return err
	}
	return r.execResolved(name, resolved, args)
}

func (r *Runner) execResolved(name string, resolved *registry.Resolved, args []string) error {
	var plan *executor.Plan
	var err error
	switch resolved.Kind {
	case registry.KindScript:
		plan, err = executor.Prepare(resolved.Path, args)
	case registry.KindAlias:
		plan, err = executor.PrepareAlias(resolved.Body, name, args)
	case registry.KindFunction:
		plan, err = executor.PrepareFunction(resolved.Body, name, args)
	default:
		return fmt.Errorf("unknown resolution kind %q", resolved.Kind)
	}
	if err != nil {
		return err
	}
	r.recordHistory(name, resolved.Kind, args)
	return plan.Exec()
}

// List implements `uc list` / `uc ls`.
func (r *Runner) List() error {
	items, err := r.Reg.List()
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Fprintln(r.Out, "Nothing registered yet — see 'uc help' to get started")
		return nil
	}

	nameWidth, kindWidth := 0, 0
	for _, it := range items {
		nameWidth = max(nameWidth, len(it.Name))
		kindWidth = max(kindWidth, len(it.Kind))
	}
	for _, it := range items {
		if reserved.Is(it.Name) {
			it.Desc = strings.TrimSpace(fmt.Sprintf("%s (hidden by the built-in 'uc %s' — rename it)", it.Desc, it.Name))
		}
		if it.Desc != "" {
			fmt.Fprintf(r.Out, "%-*s  %-*s  %s\n", nameWidth, it.Name, kindWidth, it.Kind, it.Desc)
		} else {
			fmt.Fprintf(r.Out, "%-*s  %-*s\n", nameWidth, it.Name, kindWidth, it.Kind)
		}
	}
	return nil
}

// Remove implements `uc remove` / `uc rm` — resolves name across all three
// kinds and unregisters it (architecture §3.4).
func (r *Runner) Remove(name string) error {
	resolved, err := r.resolve(name)
	if err != nil {
		return err
	}
	switch resolved.Kind {
	case registry.KindScript:
		if err := os.Remove(resolved.Path); err != nil {
			return fmt.Errorf("cannot remove %s: %s", resolved.Path, reason(err))
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
// the resolution detail (architecture §3.2, §3.4). An alias or function
// body is printed with control characters escaped: it is what will run, so
// it must be shown faithfully, but never interpreted by the terminal.
func (r *Runner) Which(name string) error {
	resolved, err := r.resolve(name)
	if err != nil {
		return err
	}
	switch resolved.Kind {
	case registry.KindScript:
		fmt.Fprintf(r.Out, "%s (script)\n", resolved.Path)
	case registry.KindAlias:
		fmt.Fprintf(r.Out, "%s (alias): %s\n", name, names.EscapeControl(resolved.Body))
	case registry.KindFunction:
		fmt.Fprintf(r.Out, "%s (function): %s\n", name, names.EscapeControl(resolved.Body))
	}
	return nil
}

// Edit implements `uc edit <name>`. Scripts: exec $EDITOR on the file
// (process replacement, does not return on success). Aliases/functions:
// delegate to the temp-file round-trip (architecture §3.4).
func (r *Runner) Edit(name string) error {
	resolved, err := r.resolve(name)
	if err != nil {
		return err
	}
	switch resolved.Kind {
	case registry.KindFunction:
		return r.FunctionEdit(name)
	case registry.KindAlias:
		return r.AliasEdit(name)
	}
	argv, err := r.editorArgv(resolved.Path)
	if err != nil {
		return err
	}
	return executor.ExecReplace(argv)
}

// Completion implements `uc completion <bash|zsh|fish>`.
func (r *Runner) Completion(shell string) error {
	script, err := completion.Script(shell)
	if err != nil {
		return err
	}
	fmt.Fprint(r.Out, script)
	return nil
}

// Complete implements the hidden `uc __complete [--describe] <partial>`. With
// completion.enabled false in settings.json it prints nothing — the shell
// function still calls back in, but gets no candidates. When describe is set
// (fish only), each described candidate is printed as "name<TAB>desc".
func (r *Runner) Complete(partial string, describe bool) error {
	if !r.Cfg.CompletionEnabled() {
		return nil
	}
	entries, err := history.Load(r.Cfg.HistoryPath())
	if err != nil {
		return err
	}
	hour, day, week, older := r.Cfg.RecencyWeights()
	weights := ranker.Weights{Hour: hour, Day: day, Week: week, Older: older}

	pairs, err := completion.DescribedCandidates(r.Reg, entries, weights, partial)
	if err != nil {
		return err
	}
	for _, p := range pairs {
		if describe && p.Desc != "" {
			fmt.Fprintf(r.Out, "%s\t%s\n", p.Name, p.Desc)
			continue
		}
		fmt.Fprintln(r.Out, p.Name)
	}
	return nil
}

// Help implements `uc help` (no arg) and `uc help <command|name>`
// (architecture §3.5). Resolution order mirrors the main dispatch order:
// reserved subcommands first, then Registry resolution.
//
// An unknown name is an error (exit 1, not the 127 of `uc <unknown>`,
// since nothing was meant to run), with the same wording and suggestions.
func (r *Runner) Help(arg string) error {
	if arg == "" {
		fmt.Fprint(r.Out, help.Overview())
		return nil
	}
	if reserved.Is(arg) && !reserved.Hidden[arg] {
		if text, ok := help.Static(arg); ok {
			fmt.Fprint(r.Out, text)
			return nil
		}
	}
	resolved, err := r.resolve(arg)
	if err != nil {
		if nf, ok := errors.AsType[*registry.NotFoundError](err); ok {
			return errors.New(nf.Error())
		}
		return err
	}
	fmt.Fprint(r.Out, help.RenderMeta(resolved.Name, resolved.Desc, resolved.Usage, resolved.Examples))
	return nil
}

// SyncInit implements `uc sync init <remote-url>`.
func (r *Runner) SyncInit(remoteURL string) error {
	return r.syncer().Init(remoteURL)
}

// SyncPush implements `uc sync push`.
func (r *Runner) SyncPush() error {
	return r.syncer().Push()
}

// SyncPull implements `uc sync pull [--force]`.
func (r *Runner) SyncPull(force bool) error {
	return r.syncer().Pull(force)
}

// SyncStatus implements `uc sync status`.
func (r *Runner) SyncStatus() error {
	return r.syncer().Status()
}

func (r *Runner) syncer() *gitsync.Syncer {
	s := gitsync.New(r.Cfg, r.Out)
	s.Warn = r.errOut()
	return s
}

// PrintVersion implements `uc version`.
func (r *Runner) PrintVersion() error {
	fmt.Fprintln(r.Out, Version)
	return nil
}

// reason turns a file error into a short plain-language cause, without
// the operation or path os.PathError repeats.
func reason(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "no such file"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	}
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		return pe.Err.Error()
	}
	if le, ok := errors.AsType[*os.LinkError](err); ok {
		return le.Err.Error()
	}
	return err.Error()
}
