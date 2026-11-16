package commands

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/eftakhairul/uc/internal/bundle"
	"github.com/eftakhairul/uc/internal/registry"
)

// plural renders "1 script" / "2 scripts".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func counts(scripts, aliases, functions int) string {
	return fmt.Sprintf("%s, %s, %s", plural(scripts, "script"), plural(aliases, "alias"), plural(functions, "function"))
}

var unsafeHostChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// defaultExportName is uc-export-<hostname>-<YYYYMMDD>.tar.gz.
func defaultExportName(now time.Time) string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	host = strings.Trim(unsafeHostChars.ReplaceAllString(host, "-"), "-.")
	if host == "" {
		host = "unknown"
	}
	return fmt.Sprintf("uc-export-%s-%s.tar.gz", host, now.Format("20060102"))
}

// Export implements `uc export [<file>] [--force]`: every script, alias,
// and function into a tar.gz. "-" writes the archive to stdout. The file is
// created 0600 via a temp file and rename, and an existing file is only
// replaced with force.
func (r *Runner) Export(path string, force bool) error {
	if err := r.Reg.Store.Migrate(); err != nil {
		return err
	}
	b, skipped, err := bundle.FromLocal(r.Cfg.ScriptsDir, r.Reg.Store)
	if err != nil {
		return err
	}
	for _, s := range skipped {
		fmt.Fprintf(r.errOut(), "uc: warning: not exported: %s\n", s)
	}
	if b.Empty() {
		return errors.New("nothing to export")
	}

	if path == "-" {
		return bundle.Write(r.Out, b)
	}
	if path == "" {
		path = defaultExportName(time.Now())
	}
	if _, err := os.Stat(path); err == nil && !force {
		return fmt.Errorf("'%s' already exists — pass --force to overwrite", path)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("cannot write %s: %s", path, reason(err))
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if err := bundle.Write(tmp, b); err != nil {
		tmp.Close()
		return fmt.Errorf("cannot write %s: %s", path, reason(err))
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cannot write %s: %s", path, reason(err))
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("cannot write %s: %s", path, reason(err))
	}

	s, a, f := b.Counts()
	fmt.Fprintf(r.Out, "exported %s to %s\n", counts(s, a, f), path)
	r.notef("the archive may contain secrets — treat it like a private key")
	return nil
}

// Import implements `uc import <file> [--force] [--dry-run]`: a merge —
// nothing local is deleted and identical entries are skipped. Any entry
// that differs from what is here blocks the whole import unless force.
// The archive is fully validated before anything is written.
func (r *Runner) Import(path string, force, dryRun bool) error {
	var src io.Reader
	if path == "-" {
		src = r.in()
	} else {
		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("cannot import '%s': %s", path, reason(err))
		}
		defer f.Close()
		src = f
	}
	incoming, err := bundle.Read(src)
	if err != nil {
		return err
	}

	if !dryRun {
		if err := r.Reg.Store.Migrate(); err != nil {
			return err
		}
	}
	local, err := bundle.Current(r.Cfg.ScriptsDir, r.Reg.Store)
	if err != nil {
		return err
	}
	changes, _ := bundle.Plan(incoming, local)
	if len(changes) == 0 {
		fmt.Fprintln(r.Out, "nothing to import — everything is already here")
		return nil
	}

	var conflicts []bundle.Change
	for _, c := range changes {
		if c.Op == '~' {
			conflicts = append(conflicts, c)
		}
	}

	if dryRun {
		for _, c := range changes {
			fmt.Fprintln(r.Out, c)
		}
		fmt.Fprintf(r.Out, "%d to add, %d to replace — nothing written (--dry-run)\n", len(changes)-len(conflicts), len(conflicts))
		return nil
	}

	if len(conflicts) > 0 && !force {
		var b strings.Builder
		if len(conflicts) == 1 {
			b.WriteString("1 entry already exists with different content:")
		} else {
			fmt.Fprintf(&b, "%d entries already exist with different content:", len(conflicts))
		}
		for _, c := range conflicts {
			fmt.Fprintf(&b, "\n  %s", c)
		}
		b.WriteString("\npass --force to replace them, or --dry-run to preview")
		return errors.New(b.String())
	}

	if err := r.Cfg.EnsureDirs(); err != nil {
		return err
	}
	if err := bundle.Apply(incoming, changes, r.Cfg.ScriptsDir, r.Reg.Store, installScript); err != nil {
		return err
	}

	var ns, na, nf int
	for _, c := range changes {
		fmt.Fprintln(r.Out, c)
		switch c.Kind {
		case "script":
			ns++
		case "alias":
			na++
		case "function":
			nf++
		}
	}
	fmt.Fprintf(r.Out, "imported %s\n", counts(ns, na, nf))
	for _, c := range changes {
		if c.Kind != "script" {
			r.warnIfShadowed(c.Name, registry.Kind(c.Kind))
		}
	}
	if ns > 0 {
		fmt.Fprintln(r.Out, "note: review scripts before running them — uc which <name>")
	}
	return nil
}
