// Package store reads and writes commands.json — the one portable file that
// holds every alias and function (improvement spec §5.2). It replaces the
// separate aliases.json and functions.json, which it still reads (and
// migrates on the first write) for installs that predate the merge.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/eftakhairul/uc/internal/atomicfile"
	"github.com/eftakhairul/uc/internal/jsonerr"
	"github.com/eftakhairul/uc/internal/names"
)

// Version is the commands.json format this build writes.
const Version = 1

// Alias maps a name to arbitrary shell command text, run via `bash -c` with
// the user's invocation args appended at the end — like bash's own alias
// expansion, so Command needs no $1/$@ itself.
type Alias struct {
	Command  string   `json:"command"`
	Desc     string   `json:"desc,omitempty"`
	Usage    string   `json:"usage,omitempty"`
	Examples []string `json:"examples,omitempty"`
}

// Function is a named, inline shell snippet run via `bash -c`; args are
// available to the body at $1/$@.
type Function struct {
	Body     string   `json:"body"`
	Desc     string   `json:"desc,omitempty"`
	Usage    string   `json:"usage,omitempty"`
	Examples []string `json:"examples,omitempty"`
}

// File is the decoded commands.json. Maps are always non-nil once returned
// by Load.
type File struct {
	Version   int                 `json:"version"`
	Aliases   map[string]Alias    `json:"aliases"`
	Functions map[string]Function `json:"functions"`

	// Entries Load set aside because their name is unusable. They are
	// invisible to lookups but written back by Save (and EncodeAll), so a
	// hand-edited entry the user was only warned about is never erased by
	// an unrelated change.
	ignoredAliases   map[string]Alias
	ignoredFunctions map[string]Function
}

// NewFile returns an empty File with non-nil maps.
func NewFile() *File {
	return &File{Version: Version, Aliases: map[string]Alias{}, Functions: map[string]Function{}}
}

// AliasNames returns the alias names, sorted.
func (f *File) AliasNames() []string { return sortedKeys(f.Aliases) }

// FunctionNames returns the function names, sorted.
func (f *File) FunctionNames() []string { return sortedKeys(f.Functions) }

// Kind reports whether name is an "alias", a "function", or "" (neither).
func (f *File) Kind(name string) string {
	if _, ok := f.Aliases[name]; ok {
		return "alias"
	}
	if _, ok := f.Functions[name]; ok {
		return "function"
	}
	return ""
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Store locates commands.json and the two legacy files it replaces.
type Store struct {
	Path            string // commands.json
	LegacyAliases   string // aliases.json
	LegacyFunctions string // functions.json
	Warn            io.Writer

	warned map[string]bool // warnings already printed by this process
}

// New returns a Store that warns on os.Stderr.
func New(path, legacyAliases, legacyFunctions string) *Store {
	return &Store{Path: path, LegacyAliases: legacyAliases, LegacyFunctions: legacyFunctions, Warn: os.Stderr}
}

// warnOnce prints a warning the first time this process sees it — Load
// runs several times per command (resolve, list, complete), and the same
// problem shouldn't be reported each time.
func (s *Store) warnOnce(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	if s.warned == nil {
		s.warned = map[string]bool{}
	}
	if s.warned[msg] {
		return
	}
	s.warned[msg] = true
	w := s.Warn
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintln(w, msg)
}

// Load returns the current contents. It never writes:
//   - commands.json exists: parse it.
//   - otherwise: build a File in memory from whichever legacy files exist.
//   - nothing exists: an empty File.
func (s *Store) Load() (*File, error) {
	data, err := os.ReadFile(s.Path)
	switch {
	case err == nil:
		return s.parse(data)
	case !errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("cannot read %s: %w", filepath.Base(s.Path), err)
	}
	return s.loadLegacy()
}

func (s *Store) parse(data []byte) (*File, error) {
	f := NewFile()
	if len(strings.TrimSpace(string(data))) == 0 {
		return f, nil
	}
	if err := json.Unmarshal(data, f); err != nil {
		return nil, fmt.Errorf("cannot read %s: %s — fix the file, or restore it with 'uc sync pull --force'",
			filepath.Base(s.Path), jsonerr.Describe(data, err))
	}
	if f.Version > Version {
		return nil, fmt.Errorf("%s was written by a newer uc (format %d) — upgrade uc", filepath.Base(s.Path), f.Version)
	}
	f.Version = Version
	if f.Aliases == nil {
		f.Aliases = map[string]Alias{}
	}
	if f.Functions == nil {
		f.Functions = map[string]Function{}
	}
	s.dropUnsafe(f, filepath.Base(s.Path))
	for name := range f.Aliases {
		if _, ok := f.Functions[name]; ok {
			s.warnOnce("uc: warning: '%s' is both an alias and a function in %s — the alias wins; remove one with 'uc function remove %s'",
				name, filepath.Base(s.Path), name)
		}
	}
	return f, nil
}

// dropUnsafe sets aside entries whose name could never be invoked (and
// could point lookups outside the scripts directory if it were). They are
// kept on the File so Save preserves them.
func (s *Store) dropUnsafe(f *File, file string) {
	for name, a := range f.Aliases {
		if !names.SafeName(name) {
			s.warnOnce("uc: warning: ignoring alias %q in %s — not a usable name; rename it in %s", name, file, file)
			if f.ignoredAliases == nil {
				f.ignoredAliases = map[string]Alias{}
			}
			f.ignoredAliases[name] = a
			delete(f.Aliases, name)
		}
	}
	for name, fn := range f.Functions {
		if !names.SafeName(name) {
			s.warnOnce("uc: warning: ignoring function %q in %s — not a usable name; rename it in %s", name, file, file)
			if f.ignoredFunctions == nil {
				f.ignoredFunctions = map[string]Function{}
			}
			f.ignoredFunctions[name] = fn
			delete(f.Functions, name)
		}
	}
}

// legacyAlias / legacyFunction decode the pre-merge files, whose metadata
// fields were nullable.
type legacyAlias struct {
	Command  string   `json:"command"`
	Desc     *string  `json:"desc"`
	Usage    *string  `json:"usage"`
	Examples []string `json:"examples"`
}

type legacyFunction struct {
	Body     string   `json:"body"`
	Desc     *string  `json:"desc"`
	Usage    *string  `json:"usage"`
	Examples []string `json:"examples"`
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func readLegacy[T any](path string) (map[string]T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read %s: %w", filepath.Base(path), err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil
	}
	var m map[string]T
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("cannot read %s: %s — fix the file by hand", filepath.Base(path), jsonerr.Describe(data, err))
	}
	return m, nil
}

func (s *Store) loadLegacy() (*File, error) {
	f := NewFile()
	if s.LegacyAliases != "" {
		m, err := readLegacy[legacyAlias](s.LegacyAliases)
		if err != nil {
			return nil, err
		}
		for name, a := range m {
			f.Aliases[name] = Alias{Command: a.Command, Desc: deref(a.Desc), Usage: deref(a.Usage), Examples: a.Examples}
		}
	}
	if s.LegacyFunctions != "" {
		m, err := readLegacy[legacyFunction](s.LegacyFunctions)
		if err != nil {
			return nil, err
		}
		for name, fn := range m {
			if _, dup := f.Aliases[name]; dup {
				s.warnOnce("uc: warning: '%s' was both an alias and a function — kept the alias", name)
				continue
			}
			f.Functions[name] = Function{Body: fn.Body, Desc: deref(fn.Desc), Usage: deref(fn.Usage), Examples: fn.Examples}
		}
	}
	s.dropUnsafe(f, "the old aliases/functions files")
	return f, nil
}

// legacyPresent lists the legacy files that exist on disk.
func (s *Store) legacyPresent() []string {
	var out []string
	for _, p := range []string{s.LegacyAliases, s.LegacyFunctions} {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Save writes commands.json atomically. When this write creates
// commands.json, any legacy file is then renamed to <name>.migrated, with a
// one-line note — the migration happens on the first write after upgrading
// and never on a read.
func (s *Store) Save(f *File) error {
	migrating := !exists(s.Path)
	data, err := EncodeAll(f)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(s.Path, data); err != nil {
		return err
	}
	if !migrating {
		return nil
	}
	var moved []string
	for _, p := range s.legacyPresent() {
		if err := os.Rename(p, p+".migrated"); err != nil {
			return fmt.Errorf("rename %s: %w", p, err)
		}
		moved = append(moved, filepath.Base(p))
	}
	if len(moved) > 0 {
		s.warnOnce("uc: note: merged %s into %s (old files kept as *.migrated)",
			strings.Join(moved, " and "), filepath.Base(s.Path))
	}
	return nil
}

// EncodeAll is Encode plus any entries Load set aside for having an
// unusable name — for writing commands.json back in place, where those
// entries must survive. Export uses Encode, which leaves them out.
func EncodeAll(f *File) ([]byte, error) {
	if len(f.ignoredAliases)+len(f.ignoredFunctions) == 0 {
		return Encode(f)
	}
	out := File{Version: f.Version, Aliases: map[string]Alias{}, Functions: map[string]Function{}}
	for name, a := range f.ignoredAliases {
		out.Aliases[name] = a
	}
	for name, fn := range f.ignoredFunctions {
		out.Functions[name] = fn
	}
	for name, a := range f.Aliases {
		out.Aliases[name] = a
	}
	for name, fn := range f.Functions {
		out.Functions[name] = fn
	}
	return Encode(&out)
}

// Encode renders f as commands.json content: indented, current Version,
// non-nil maps, unset metadata omitted.
func Encode(f *File) ([]byte, error) {
	out := *f
	out.Version = Version
	if out.Aliases == nil {
		out.Aliases = map[string]Alias{}
	}
	if out.Functions == nil {
		out.Functions = map[string]Function{}
	}
	// No HTML escaping: commands.json is edited by hand and holds shell
	// text, where "&&" must not turn into "\u0026\u0026".
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(&out); err != nil {
		return nil, fmt.Errorf("encode commands.json: %w", err)
	}
	return buf.Bytes(), nil
}

// Update runs a locked load-modify-save cycle: fn edits the loaded File and
// the result is saved, unless fn returns an error, in which case nothing is
// written.
func (s *Store) Update(fn func(f *File) error) error {
	return atomicfile.WithLock(s.Path, func() error {
		f, err := s.Load()
		if err != nil {
			return err
		}
		if err := fn(f); err != nil {
			return err
		}
		return s.Save(f)
	})
}

// Migrate writes commands.json when only legacy files exist; otherwise it
// does nothing. Sync, export, and import call it so they always operate on
// commands.json.
func (s *Store) Migrate() error {
	if exists(s.Path) || len(s.legacyPresent()) == 0 {
		return nil
	}
	return s.Update(func(*File) error { return nil })
}

// SetAlias creates or replaces the alias name. It fails if name is a
// function: the two kinds share one namespace.
func (s *Store) SetAlias(name string, a Alias) error {
	return s.Update(func(f *File) error {
		if _, ok := f.Functions[name]; ok {
			return fmt.Errorf("'%s' is already a function — remove it first with 'uc remove %s'", name, name)
		}
		f.Aliases[name] = a
		return nil
	})
}

// SetFunction creates or replaces the function name. It fails if name is
// an alias.
func (s *Store) SetFunction(name string, fn Function) error {
	return s.Update(func(f *File) error {
		if _, ok := f.Aliases[name]; ok {
			return fmt.Errorf("'%s' is already an alias — remove it first with 'uc remove %s'", name, name)
		}
		f.Functions[name] = fn
		return nil
	})
}

// RemoveAlias deletes the alias name, erroring if there is none.
func (s *Store) RemoveAlias(name string) error {
	return s.Update(func(f *File) error {
		if _, ok := f.Aliases[name]; !ok {
			return fmt.Errorf("no alias named '%s'", name)
		}
		delete(f.Aliases, name)
		return nil
	})
}

// RemoveFunction deletes the function name, erroring if there is none.
func (s *Store) RemoveFunction(name string) error {
	return s.Update(func(f *File) error {
		if _, ok := f.Functions[name]; !ok {
			return fmt.Errorf("no function named '%s'", name)
		}
		delete(f.Functions, name)
		return nil
	})
}

// Remove deletes name, whichever kind it is, and reports which it was.
func (s *Store) Remove(name string) (kind string, err error) {
	err = s.Update(func(f *File) error {
		kind = f.Kind(name)
		switch kind {
		case "alias":
			delete(f.Aliases, name)
		case "function":
			delete(f.Functions, name)
		default:
			return fmt.Errorf("no alias or function named '%s'", name)
		}
		return nil
	})
	return kind, err
}
