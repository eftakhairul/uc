// Package bundle implements the `uc export` / `uc import` archive: a
// gzip-compressed tar with the same layout as the sync repo,
//
//	commands.json
//	scripts/<name>
//
// Reading is the security-sensitive half, since an archive may come from
// someone else: Read validates the whole archive before anything is
// returned, and rejects it outright on the first problem.
package bundle

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/eftakhairul/uc/internal/jsonerr"
	"github.com/eftakhairul/uc/internal/names"
	"github.com/eftakhairul/uc/internal/store"
)

// Limits on what Read accepts.
const (
	MaxFileBytes  = 1 << 20  // per entry; same cap as `uc add <url>`
	MaxEntries    = 1000     // tar entries
	MaxTotalBytes = 50 << 20 // uncompressed
)

const (
	commandsName = "commands.json"
	scriptsDir   = "scripts"
)

// Bundle is the portable part of a uc installation, held in memory.
type Bundle struct {
	Scripts  map[string][]byte // file name (with extension) -> content
	Commands *store.File
}

// Empty reports whether b holds nothing.
func (b *Bundle) Empty() bool {
	return len(b.Scripts) == 0 && (b.Commands == nil || len(b.Commands.Aliases)+len(b.Commands.Functions) == 0)
}

// Counts returns how many scripts, aliases, and functions b holds.
func (b *Bundle) Counts() (scripts, aliases, functions int) {
	scripts = len(b.Scripts)
	if b.Commands != nil {
		aliases, functions = len(b.Commands.Aliases), len(b.Commands.Functions)
	}
	return
}

func isRegular(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// FromLocal reads the live scripts directory and commands.json for export.
// Entries an import would refuse (an invalid name, an oversized script, an
// empty alias) are left out and described in skipped, so an export always
// produces an archive that can be imported.
func FromLocal(scripts string, st *store.Store) (b *Bundle, skipped []string, err error) {
	return load(scripts, st, true)
}

// Current reads everything on this machine — nothing skipped — for an
// import to compare against: an entry export would leave out still exists
// here and must count as a conflict, not as absent.
func Current(scripts string, st *store.Store) (*Bundle, error) {
	b, _, err := load(scripts, st, false)
	return b, err
}

func load(scripts string, st *store.Store, strict bool) (b *Bundle, skipped []string, err error) {
	b = &Bundle{Scripts: map[string][]byte{}}
	entries, err := os.ReadDir(scripts)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("cannot read %s: %w", scripts, err)
	}
	for _, e := range entries {
		name := e.Name()
		path := filepath.Join(scripts, name)
		if strings.HasPrefix(name, ".") || !isRegular(path) {
			continue
		}
		if strict && names.ValidName(name) != nil {
			skipped = append(skipped, fmt.Sprintf("script '%s' — not a valid name", name))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, nil, fmt.Errorf("cannot read %s: %w", path, err)
		}
		if strict && len(data) > MaxFileBytes {
			skipped = append(skipped, fmt.Sprintf("script '%s' — larger than 1 MB", name))
			continue
		}
		b.Scripts[name] = data
	}

	f, err := st.Load()
	if err != nil {
		return nil, nil, err
	}
	if !strict {
		b.Commands = f
		return b, nil, nil
	}
	out := store.NewFile()
	for _, name := range f.AliasNames() {
		a := f.Aliases[name]
		switch {
		case names.ValidName(name) != nil:
			skipped = append(skipped, fmt.Sprintf("alias '%s' — not a valid name", name))
		case strings.TrimSpace(a.Command) == "":
			skipped = append(skipped, fmt.Sprintf("alias '%s' — empty command", name))
		default:
			out.Aliases[name] = a
		}
	}
	for _, name := range f.FunctionNames() {
		fn := f.Functions[name]
		switch {
		case names.ValidName(name) != nil:
			skipped = append(skipped, fmt.Sprintf("function '%s' — not a valid name", name))
		case strings.TrimSpace(fn.Body) == "":
			skipped = append(skipped, fmt.Sprintf("function '%s' — empty body", name))
		case out.Kind(name) != "":
			skipped = append(skipped, fmt.Sprintf("function '%s' — an alias has the same name", name))
		default:
			out.Functions[name] = fn
		}
	}
	b.Commands = out
	return b, skipped, nil
}

// Write encodes b as tar.gz. Entries are sorted by name; mode 0644 for
// commands.json and 0755 for scripts; owner 0; ModTime now. The modes are
// informational: Read never applies them.
func Write(w io.Writer, b *Bundle) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	now := time.Now().UTC().Truncate(time.Second)

	add := func(name string, mode int64, data []byte) error {
		hdr := &tar.Header{Name: name, Mode: mode, Size: int64(len(data)), ModTime: now, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err := tw.Write(data)
		return err
	}

	if b.Commands != nil && len(b.Commands.Aliases)+len(b.Commands.Functions) > 0 {
		data, err := store.Encode(b.Commands)
		if err != nil {
			return err
		}
		if err := add(commandsName, 0o644, data); err != nil {
			return err
		}
	}
	if len(b.Scripts) > 0 {
		if err := tw.WriteHeader(&tar.Header{Name: scriptsDir + "/", Mode: 0o755, ModTime: now, Typeflag: tar.TypeDir, Format: tar.FormatPAX}); err != nil {
			return err
		}
		for _, name := range sortedKeys(b.Scripts) {
			if err := add(scriptsDir+"/"+name, 0o755, b.Scripts[name]); err != nil {
				return err
			}
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ImportError is an archive rejected by Read.
type ImportError struct{ Reason string }

func (e *ImportError) Error() string { return "cannot import: " + e.Reason }

func reject(format string, a ...any) error {
	return &ImportError{Reason: fmt.Sprintf(format, a...)}
}

var errTooLarge = errors.New("archive is too large")

// capReader fails once more than n bytes have been read, so a
// decompression bomb is cut off rather than buffered.
type capReader struct {
	r io.Reader
	n int64
}

func (c *capReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n -= int64(n)
	if c.n < 0 {
		return n, errTooLarge
	}
	return n, err
}

// Read decodes and fully validates an archive. Every failure rejects the
// whole archive with an *ImportError. Modes, owners, and timestamps in the
// archive are ignored.
func Read(r io.Reader) (*Bundle, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, reject("not a uc export file")
	}
	defer gz.Close()
	// Tar headers and padding count against the budget too, which only
	// makes it stricter.
	tr := tar.NewReader(&capReader{r: gz, n: MaxTotalBytes + 64<<10})

	b := &Bundle{Scripts: map[string][]byte{}}
	seen := map[string]bool{}
	var commandsData []byte
	entries := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if errors.Is(err, errTooLarge) {
				return nil, reject("archive is too large")
			}
			return nil, reject("not a uc export file")
		}
		entries++
		if entries > MaxEntries {
			return nil, reject("too many entries")
		}
		name := hdr.Name
		if seen[name] {
			return nil, reject("'%s' appears twice", name)
		}
		seen[name] = true

		switch hdr.Typeflag {
		case tar.TypeDir:
			if name != scriptsDir+"/" && name != scriptsDir {
				return nil, reject("unexpected entry '%s'", name)
			}
			continue
		case tar.TypeReg, tar.TypeRegA:
		default:
			return nil, reject("'%s' is not a regular file", name)
		}

		var script string
		switch {
		case name == commandsName:
		case strings.HasPrefix(name, scriptsDir+"/"):
			script = strings.TrimPrefix(name, scriptsDir+"/")
			if script == "" || strings.ContainsAny(script, `/\`) || script == "." || script == ".." {
				return nil, reject("unexpected entry '%s'", name)
			}
		default:
			return nil, reject("unexpected entry '%s'", name)
		}

		if hdr.Size > MaxFileBytes {
			return nil, reject("'%s' is larger than 1 MB", name)
		}
		data, err := io.ReadAll(io.LimitReader(tr, MaxFileBytes+1))
		if err != nil {
			if errors.Is(err, errTooLarge) {
				return nil, reject("archive is too large")
			}
			return nil, reject("not a uc export file")
		}
		if len(data) > MaxFileBytes {
			return nil, reject("'%s' is larger than 1 MB", name)
		}

		if script == "" {
			commandsData = data
			continue
		}
		if names.ValidName(script) != nil {
			return nil, reject("invalid script name '%s'", script)
		}
		b.Scripts[script] = data
	}

	bare := map[string]string{}
	for _, name := range sortedKeys(b.Scripts) {
		if other, ok := bare[names.BareName(name)]; ok {
			return nil, reject("'%s' and '%s' would both run as '%s'", other, name, names.BareName(name))
		}
		bare[names.BareName(name)] = name
	}

	b.Commands = store.NewFile()
	if commandsData != nil {
		f, err := parseCommands(commandsData)
		if err != nil {
			return nil, err
		}
		b.Commands = f
	}
	if b.Empty() {
		return nil, reject("the archive is empty")
	}
	return b, nil
}

// parseCommands validates an archive's commands.json and cleans its
// metadata.
func parseCommands(data []byte) (*store.File, error) {
	f := store.NewFile()
	if err := json.Unmarshal(data, f); err != nil {
		return nil, reject("commands.json is not valid: %s", jsonerr.Describe(data, err))
	}
	if f.Version > store.Version {
		return nil, reject("written by a newer uc — upgrade uc")
	}
	f.Version = store.Version
	if f.Aliases == nil {
		f.Aliases = map[string]store.Alias{}
	}
	if f.Functions == nil {
		f.Functions = map[string]store.Function{}
	}
	for _, name := range sortedKeys(f.Aliases) {
		a := f.Aliases[name]
		if names.ValidName(name) != nil {
			return nil, reject("invalid name '%s' in commands.json", name)
		}
		if strings.TrimSpace(a.Command) == "" {
			return nil, reject("'%s' has an empty command", name)
		}
		if _, ok := f.Functions[name]; ok {
			return nil, reject("'%s' is both an alias and a function", name)
		}
		a.Desc, a.Usage, a.Examples = names.CleanMeta(a.Desc), names.CleanMeta(a.Usage), cleanAll(a.Examples)
		f.Aliases[name] = a
	}
	for _, name := range sortedKeys(f.Functions) {
		fn := f.Functions[name]
		if names.ValidName(name) != nil {
			return nil, reject("invalid name '%s' in commands.json", name)
		}
		if strings.TrimSpace(fn.Body) == "" {
			return nil, reject("'%s' has an empty command", name)
		}
		fn.Desc, fn.Usage, fn.Examples = names.CleanMeta(fn.Desc), names.CleanMeta(fn.Usage), cleanAll(fn.Examples)
		f.Functions[name] = fn
	}
	return f, nil
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

// Change is one difference an import would make.
type Change struct {
	Op   byte   // '+' new, '~' differs from what is here
	Kind string // "script", "alias", "function"
	Name string // script file name, or alias/function name
}

func (c Change) String() string { return fmt.Sprintf("%c %s %s", c.Op, c.Kind, c.Name) }

// Plan compares an incoming bundle with the local one. same counts entries
// that are identical and will be skipped. A script whose bare name matches
// a local script under another extension, and a name that is the other
// kind here, are conflicts ('~'): importing them would change what that
// name runs.
func Plan(incoming, local *Bundle) (changes []Change, same int) {
	localBare := map[string]string{}
	for name := range local.Scripts {
		localBare[names.BareName(name)] = name
	}
	for _, name := range sortedKeys(incoming.Scripts) {
		data := incoming.Scripts[name]
		if cur, ok := local.Scripts[name]; ok {
			if bytes.Equal(cur, data) {
				same++
			} else {
				changes = append(changes, Change{'~', "script", name})
			}
			continue
		}
		if _, ok := localBare[names.BareName(name)]; ok {
			changes = append(changes, Change{'~', "script", name})
			continue
		}
		changes = append(changes, Change{'+', "script", name})
	}

	lf, inf := local.Commands, incoming.Commands
	if lf == nil {
		lf = store.NewFile()
	}
	if inf == nil {
		inf = store.NewFile()
	}
	for _, name := range inf.AliasNames() {
		a := inf.Aliases[name]
		switch cur, ok := lf.Aliases[name]; {
		case ok && aliasEqual(cur, a):
			same++
		case ok || lf.Kind(name) != "":
			changes = append(changes, Change{'~', "alias", name})
		default:
			changes = append(changes, Change{'+', "alias", name})
		}
	}
	for _, name := range inf.FunctionNames() {
		fn := inf.Functions[name]
		switch cur, ok := lf.Functions[name]; {
		case ok && functionEqual(cur, fn):
			same++
		case ok || lf.Kind(name) != "":
			changes = append(changes, Change{'~', "function", name})
		default:
			changes = append(changes, Change{'+', "function", name})
		}
	}
	return changes, same
}

func aliasEqual(a, b store.Alias) bool {
	return a.Command == b.Command && a.Desc == b.Desc && a.Usage == b.Usage && slices.Equal(a.Examples, b.Examples)
}

func functionEqual(a, b store.Function) bool {
	return a.Body == b.Body && a.Desc == b.Desc && a.Usage == b.Usage && slices.Equal(a.Examples, b.Examples)
}

// Apply writes the planned changes. Scripts go through install (temp file
// + rename, with uc's own mode); a local script with the same bare name
// under another extension is removed so the name stays unambiguous.
// Aliases and functions are merged into the store and saved once; an entry
// of the other kind under the same name is replaced.
func Apply(incoming *Bundle, changes []Change, scripts string, st *store.Store, install func(dest string, src io.Reader) error) error {
	var cmds []Change
	for _, c := range changes {
		if c.Kind != "script" {
			cmds = append(cmds, c)
			continue
		}
		if err := removeOtherExtensions(scripts, c.Name); err != nil {
			return err
		}
		if err := install(filepath.Join(scripts, c.Name), bytes.NewReader(incoming.Scripts[c.Name])); err != nil {
			return fmt.Errorf("write %s: %w", c.Name, err)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return st.Update(func(f *store.File) error {
		for _, c := range cmds {
			delete(f.Aliases, c.Name)
			delete(f.Functions, c.Name)
			if c.Kind == "alias" {
				f.Aliases[c.Name] = incoming.Commands.Aliases[c.Name]
			} else {
				f.Functions[c.Name] = incoming.Commands.Functions[c.Name]
			}
		}
		return nil
	})
}

func removeOtherExtensions(scripts, name string) error {
	bare := names.BareName(name)
	for _, ext := range names.Exts {
		other := bare + ext
		if other == name {
			continue
		}
		p := filepath.Join(scripts, other)
		if isRegular(p) {
			if err := os.Remove(p); err != nil {
				return fmt.Errorf("remove %s: %w", p, err)
			}
		}
	}
	return nil
}
