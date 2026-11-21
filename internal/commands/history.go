package commands

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/eftakhairul/uc/internal/history"
	"github.com/eftakhairul/uc/internal/registry"
)

const historyOffMsg = "history is turned off (history_size is 0)"

// recordHistory appends a run to history.json unless history is off. A
// failure is a warning: it must never stop the run itself.
func (r *Runner) recordHistory(name string, kind registry.Kind, args []string) {
	size := r.Cfg.HistorySize()
	if size <= 0 {
		return
	}
	entry := history.Entry{Name: name, Args: args, Timestamp: time.Now().UTC(), Kind: string(kind)}
	if err := history.Append(r.Cfg.HistoryPath(), entry, size); err != nil {
		fmt.Fprintf(r.errOut(), "uc: warning: could not record history: %v\n", err)
	}
}

// History implements `uc history [n]` — prints the last n entries
// (default/max: the configured history_size), most recent first, with
// relative timestamps and kind. n <= 0 means the default.
func (r *Runner) History(n int) error {
	limit := r.Cfg.HistorySize()
	if limit <= 0 {
		fmt.Fprintln(r.Out, historyOffMsg)
		return nil
	}
	entries, err := history.Load(r.Cfg.HistoryPath())
	if err != nil {
		return err
	}
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
	if r.Cfg.HistorySize() <= 0 {
		return fmt.Errorf("%s", historyOffMsg)
	}
	entries, err := history.Load(r.Cfg.HistoryPath())
	if err != nil {
		return err
	}
	switch {
	case len(entries) == 0:
		return fmt.Errorf("no history entry #%d — history is empty", n)
	case n < 1 || n > len(entries):
		return fmt.Errorf("no history entry #%d — there are %d", n, len(entries))
	}
	// entries is oldest-first; #1 is the most recent, i.e. the last element.
	e := entries[len(entries)-n]
	return r.Run(e.Name, e.Args)
}

// HistoryClear implements `uc history clear`.
func (r *Runner) HistoryClear() error {
	if err := history.Clear(r.Cfg.HistoryPath()); err != nil {
		return err
	}
	fmt.Fprintln(r.Out, "history cleared")
	return nil
}

func relativeTime(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
