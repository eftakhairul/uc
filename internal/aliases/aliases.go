// Package aliases reads and writes aliases.json — user-curated shortcuts
// mapping a name to arbitrary shell command text, the same idea as a bash
// alias (architecture §3.2b).
package aliases

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/eftakhairul/uc/internal/atomicfile"
)

// Alias is one entry in aliases.json. Command is raw shell text, run via
// `bash -c` with the user's invocation args auto-appended at the end (like
// bash's own alias expansion, e.g. `alias gs='git status'` then `gs -s` runs
// `git status -s`) — no $1/$@ needed in Command itself, unlike a function
// body. Desc/Usage are pointers so "unset" is distinguishable from an
// explicit empty string, same spirit as config.Settings (architecture
// §3.2b).
type Alias struct {
	Command  string   `json:"command"`
	Desc     *string  `json:"desc"`
	Usage    *string  `json:"usage"`
	Examples []string `json:"examples"`
}

// Load reads all aliases from path. A missing file yields an empty map,
// not an error (architecture §6, decision 13's "no auto-create" spirit
// applies here too).
func Load(path string) (map[string]Alias, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Alias{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return map[string]Alias{}, nil
	}
	var m map[string]Alias
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if m == nil {
		m = map[string]Alias{}
	}
	return m, nil
}

// Add creates or overwrites the alias named name and saves it.
func Add(path, name string, a Alias) error {
	m, err := Load(path)
	if err != nil {
		return err
	}
	m[name] = a
	return save(path, m)
}

// Remove deletes the alias named name, erroring if it doesn't exist.
func Remove(path, name string) error {
	m, err := Load(path)
	if err != nil {
		return err
	}
	if _, ok := m[name]; !ok {
		return fmt.Errorf("no alias named %q", name)
	}
	delete(m, name)
	return save(path, m)
}

// Names returns m's keys, sorted alphabetically.
func Names(m map[string]Alias) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func save(path string, m map[string]Alias) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal aliases: %w", err)
	}
	return atomicfile.Write(path, data)
}
