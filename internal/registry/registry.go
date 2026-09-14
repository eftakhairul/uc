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
	"slices"
	"sort"
	"strings"

	"github.com/eftakhairul/uc/internal/aliases"
	"github.com/eftakhairul/uc/internal/functions"
)

// extPriority is the order in which extensions are tried when a bare name
// is resolved (architecture §3.2). Order matters: first match wins.
var extPriority = []string{"", ".sh", ".py", ".js", ".rb", ".pl"}

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
// aliases.json/functions.json config files (architecture §3.2).
type Registry struct {
	ScriptsDir    string
	AliasesPath   string
	FunctionsPath string
}

func New(scriptsDir, aliasesPath, functionsPath string) *Registry {
	return &Registry{ScriptsDir: scriptsDir, AliasesPath: aliasesPath, FunctionsPath: functionsPath}
}

// AmbiguousNameError is returned by Resolve when a bare name matches more
// than one script file with different extensions (architecture §6, decision 2).
type AmbiguousNameError struct {
	Name  string
	Paths []string
}

func (e *AmbiguousNameError) Error() string {
	return fmt.Sprintf("ambiguous name %q: matches %s", e.Name, strings.Join(e.Paths, ", "))
}

// NotFoundError is returned by Resolve when no script, alias, or function
// matches the name.
type NotFoundError struct {
	Name string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("no command named %q", e.Name)
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

	Desc     string
	Usage    string
	Examples []string
}

// ResolveScript resolves name against the scripts directory only, trying
// extensions in extPriority order. This is also used to validate alias
// targets, which are restricted to registered scripts (architecture §3.2b).
func (r *Registry) ResolveScript(name string) (string, error) {
	var matches []string
	for _, ext := range extPriority {
		p := filepath.Join(r.ScriptsDir, name+ext)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
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
	path, err := r.ResolveScript(name)
	switch {
	case err == nil:
		meta, _ := ExtractMeta(path)
		return &Resolved{Kind: KindScript, Name: name, Path: path, Desc: meta.Desc, Usage: meta.Usage, Examples: meta.Examples}, nil
	case isAmbiguous(err):
		return nil, err
	}

	aliasMap, err := aliases.Load(r.AliasesPath)
	if err != nil {
		return nil, err
	}
	if a, ok := aliasMap[name]; ok {
		return &Resolved{
			Kind: KindAlias, Name: name, Body: a.Command,
			Desc: derefOr(a.Desc), Usage: derefOr(a.Usage), Examples: a.Examples,
		}, nil
	}

	functionMap, err := functions.Load(r.FunctionsPath)
	if err != nil {
		return nil, err
	}
	if f, ok := functionMap[name]; ok {
		return &Resolved{
			Kind: KindFunction, Name: name, Body: f.Body,
			Desc: derefOr(f.Desc), Usage: derefOr(f.Usage), Examples: f.Examples,
		}, nil
	}

	return nil, &NotFoundError{Name: name}
}

func isAmbiguous(err error) bool {
	_, ok := err.(*AmbiguousNameError)
	return ok
}

func derefOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Item describes one entry in a unified listing (architecture §3.4, §4).
type Item struct {
	Name string
	Kind Kind
	Desc string
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

	aliasMap, err := aliases.Load(r.AliasesPath)
	if err != nil {
		return nil, err
	}
	functionMap, err := functions.Load(r.FunctionsPath)
	if err != nil {
		return nil, err
	}

	items := make([]Item, 0, len(scripts)+len(aliasMap)+len(functionMap))
	seen := make(map[string]bool)
	for _, s := range scripts {
		if seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		items = append(items, Item{Name: s.Name, Kind: KindScript, Desc: s.Desc})
	}
	for name, a := range aliasMap {
		if seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, Item{Name: name, Kind: KindAlias, Desc: derefOr(a.Desc)})
	}
	for name, f := range functionMap {
		if seen[name] {
			continue
		}
		items = append(items, Item{Name: name, Kind: KindFunction, Desc: derefOr(f.Desc)})
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

func (r *Registry) listScripts() ([]script, error) {
	entries, err := os.ReadDir(r.ScriptsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", r.ScriptsDir, err)
	}

	var scripts []script
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(r.ScriptsDir, e.Name())
		name := BareName(e.Name())
		meta, err := ExtractMeta(path)
		if err != nil {
			// Unreadable metadata must not hide a registered script: list it
			// without a description rather than dropping it, so it doesn't
			// look unregistered while still being runnable.
			meta = Meta{}
		}
		scripts = append(scripts, script{Name: name, Path: path, Desc: meta.Desc})
	}

	sort.Slice(scripts, func(i, j int) bool { return scripts[i].Name < scripts[j].Name })
	return scripts, nil
}

// BareName strips a known script extension from a filename, if present —
// the name a registered script is invoked by.
func BareName(filename string) string {
	ext := filepath.Ext(filename)
	if slices.Contains(extPriority[1:], ext) { // skip "" (no-extension case)
		return strings.TrimSuffix(filename, ext)
	}
	return filename
}

// Meta holds a script's parsed metadata tags (architecture §3.5).
type Meta struct {
	Desc     string
	Usage    string
	Examples []string
}

// ExtractMeta scans the first maxMetaLines lines of a file for
// "# @desc: ...", "# @usage: ...", and repeatable "# @example: ..."
// comments (architecture §3.5). Malformed or repeated non-repeatable tags
// are silently ignored (first occurrence wins) rather than erroring
// (architecture §6, decision 20).
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
		tag, value := match[1], match[2]
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
