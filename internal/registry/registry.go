// Package registry resolves names to their underlying script, alias, or
// function (architecture §3.2) and extracts script metadata tags.
package registry

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/eftakhairul/uc/internal/names"
	"github.com/eftakhairul/uc/internal/store"
)

// maxMetaLines is how many lines from the top of a script are scanned for
// "# @<tag>: ..." comments (architecture §3.5).
const maxMetaLines = 25

var tagRe = regexp.MustCompile(`^\s*#\s*@(\w+):\s*(.*?)\s*$`)

// Kind identifies which of the three resolution tiers a name resolved to
// (architecture §3.2).
type Kind string

const (
	KindScript   Kind = "script"
	KindAlias    Kind = "alias"
	KindFunction Kind = "function"
)

// Registry resolves names within one scripts directory plus the
// commands.json store of aliases and functions.
type Registry struct {
	ScriptsDir string
	Store      *store.Store
}

func New(scriptsDir string, st *store.Store) *Registry {
	return &Registry{ScriptsDir: scriptsDir, Store: st}
}

// AmbiguousNameError is returned by Resolve when a bare name matches more
// than one script file with different extensions (architecture §6, decision 2).
type AmbiguousNameError struct {
	Name  string
	Paths []string
}

func (e *AmbiguousNameError) Error() string {
	example := e.Name
	if len(e.Paths) > 0 {
		example = filepath.Base(e.Paths[len(e.Paths)-1])
	}
	return fmt.Sprintf("ambiguous name '%s': matches %s — remove one, for example: uc remove %s",
		e.Name, strings.Join(e.Paths, ", "), example)
}

// NotFoundError is returned by Resolve when no script, alias, or function
// matches the name. Similar holds near-miss names to suggest; callers that
// know the full set of names fill it in.
type NotFoundError struct {
	Name    string
	Similar []string
}

func (e *NotFoundError) Error() string {
	msg := fmt.Sprintf("'%s' is not a uc command or a registered name", e.Name)
	switch len(e.Similar) {
	case 0:
		return msg + " — see 'uc list'"
	case 1:
		return msg + ".\n\nThe most similar name is\n\t" + e.Similar[0]
	default:
		return msg + ".\n\nThe most similar names are\n\t" + strings.Join(e.Similar, "\n\t")
	}
}

// Resolved is the outcome of resolving a name to one of the three tiers.
type Resolved struct {
	Kind Kind
	Name string

	// Path is the script's absolute path (KindScript only).
	Path string
	// Body is the raw shell text to run via bash -c: an alias's command
	// (KindAlias, user args auto-appended) or a function's inline snippet
	// (KindFunction, args available at $1/$@ if the body references them).
	Body string

	// Desc, Usage, and Examples are cleaned of control characters and
	// safe to print.
	Desc     string
	Usage    string
	Examples []string
}

// isRegularFile reports whether path is a regular file, following a
// symlink to its target.
func isRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// ResolveScript resolves name against the scripts directory only, trying
// extensions in names.Exts order. A name that could escape the scripts
// directory ("../x", "a/b") or name a dotfile is never found.
func (r *Registry) ResolveScript(name string) (string, error) {
	if !names.SafeName(name) {
		return "", &NotFoundError{Name: name}
	}
	var matches []string
	for _, ext := range names.Exts {
		p := filepath.Join(r.ScriptsDir, name+ext)
		if isRegularFile(p) {
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		return "", &NotFoundError{Name: name}
	case 1:
		return matches[0], nil
	default:
		return "", &AmbiguousNameError{Name: name, Paths: matches}
	}
}

// Resolve resolves name across all three tiers, in precedence order:
// script, then alias, then function (architecture §3.2).
func (r *Registry) Resolve(name string) (*Resolved, error) {
	if !names.SafeName(name) {
		return nil, &NotFoundError{Name: name}
	}
	path, err := r.ResolveScript(name)
	switch {
	case err == nil:
		meta, _ := ExtractMeta(path)
		return &Resolved{Kind: KindScript, Name: name, Path: path, Desc: meta.Desc, Usage: meta.Usage, Examples: meta.Examples}, nil
	case isAmbiguous(err):
		return nil, err
	}

	f, err := r.Store.Load()
	if err != nil {
		return nil, err
	}
	if a, ok := f.Aliases[name]; ok {
		return &Resolved{
			Kind: KindAlias, Name: name, Body: a.Command,
			Desc: names.CleanMeta(a.Desc), Usage: names.CleanMeta(a.Usage), Examples: cleanAll(a.Examples),
		}, nil
	}
	if fn, ok := f.Functions[name]; ok {
		return &Resolved{
			Kind: KindFunction, Name: name, Body: fn.Body,
			Desc: names.CleanMeta(fn.Desc), Usage: names.CleanMeta(fn.Usage), Examples: cleanAll(fn.Examples),
		}, nil
	}
	return nil, &NotFoundError{Name: name}
}

func cleanAll(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = names.CleanMeta(s)
	}
	return out
}

func isAmbiguous(err error) bool {
	_, ok := errors.AsType[*AmbiguousNameError](err)
	return ok
}

// Item describes one entry in a unified listing (architecture §3.4, §4).
type Item struct {
	Name string
	Kind Kind
	Desc string // cleaned, safe to print
}

// List enumerates all registered scripts, aliases, and functions together,
// sorted by name (architecture §3.4, §4). Each name appears once: a name
// registered as more than one kind is listed only under the kind Resolve
// would pick (script > alias > function precedence), since that's the only
// one runnable under that name.
func (r *Registry) List() ([]Item, error) {
	scripts, err := r.listScripts()
	if err != nil {
		return nil, err
	}
	f, err := r.Store.Load()
	if err != nil {
		return nil, err
	}

	items := make([]Item, 0, len(scripts)+len(f.Aliases)+len(f.Functions))
	seen := make(map[string]bool)
	for _, s := range scripts {
		if seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		items = append(items, Item{Name: s.Name, Kind: KindScript, Desc: s.Desc})
	}
	for name, a := range f.Aliases {
		if seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, Item{Name: name, Kind: KindAlias, Desc: names.CleanMeta(a.Desc)})
	}
	for name, fn := range f.Functions {
		if seen[name] {
			continue
		}
		items = append(items, Item{Name: name, Kind: KindFunction, Desc: names.CleanMeta(fn.Desc)})
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items, nil
}

// script describes one registered script file, prior to merging with
// aliases/functions in List.
type script struct {
	Name string
	Path string
	Desc string
}

// listScripts returns the scripts directory's runnable entries: regular
// files and symlinks to regular files. Dotfiles (.DS_Store, a leftover
// .name.tmp-* from an interrupted add) and subdirectories are skipped.
func (r *Registry) listScripts() ([]script, error) {
	entries, err := os.ReadDir(r.ScriptsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read %s: %w", r.ScriptsDir, err)
	}

	var scripts []script
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(r.ScriptsDir, e.Name())
		switch {
		case e.Type().IsRegular():
		case e.Type()&os.ModeSymlink != 0 && isRegularFile(path):
		default:
			continue
		}
		meta, err := ExtractMeta(path)
		if err != nil {
			// Unreadable metadata must not hide a registered script: list it
			// without a description rather than dropping it, so it doesn't
			// look unregistered while still being runnable.
			meta = Meta{}
		}
		scripts = append(scripts, script{Name: BareName(e.Name()), Path: path, Desc: meta.Desc})
	}

	sort.Slice(scripts, func(i, j int) bool { return scripts[i].Name < scripts[j].Name })
	return scripts, nil
}

// BareName strips a known script extension from a filename, if present —
// the name a registered script is invoked by.
func BareName(filename string) string { return names.BareName(filename) }

// Meta holds a script's parsed metadata tags (architecture §3.5), cleaned
// of control characters.
type Meta struct {
	Desc     string
	Usage    string
	Examples []string
}

// ExtractMeta scans the first maxMetaLines lines of a file for
// "# @desc: ...", "# @usage: ...", and repeatable "# @example: ..."
// comments (architecture §3.5). Malformed or repeated non-repeatable tags
// are silently ignored (first occurrence wins) rather than erroring
// (architecture §6, decision 20). Values pass through names.CleanMeta, so a
// downloaded script can't smuggle terminal escape sequences into uc's
// output.
func ExtractMeta(path string) (Meta, error) {
	f, err := os.Open(path)
	if err != nil {
		return Meta{}, err
	}
	defer f.Close()

	var m Meta
	scanner := bufio.NewScanner(f)
	for i := 0; i < maxMetaLines && scanner.Scan(); i++ {
		match := tagRe.FindStringSubmatch(scanner.Text())
		if match == nil {
			continue
		}
		tag, value := match[1], names.CleanMeta(match[2])
		switch tag {
		case "desc":
			if m.Desc == "" {
				m.Desc = value
			}
		case "usage":
			if m.Usage == "" {
				m.Usage = value
			}
		case "example":
			m.Examples = append(m.Examples, value)
		}
	}
	// A line too long for the scanner's buffer (a minified file, an
	// accidentally registered binary) can't be a "# @tag:" comment, so it
	// ends the metadata scan rather than failing it — any tags already
	// collected are still valid.
	if err := scanner.Err(); err != nil && !errors.Is(err, bufio.ErrTooLong) {
		return m, err
	}
	return m, nil
}
