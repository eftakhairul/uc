// Package functions reads and writes functions.json — named, inline shell
// snippets stored directly in uc's own config, run via `bash -c`
// (architecture §3.2c).
package functions

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/eftakhairul/uc/internal/atomicfile"
)

// Function is one entry in functions.json. Desc/Usage are pointers so
// "unset" is distinguishable from an explicit empty string.
type Function struct {
	Body     string   `json:"body"`
	Desc     *string  `json:"desc"`
	Usage    *string  `json:"usage"`
	Examples []string `json:"examples"`
}

// Load reads all functions from path. A missing file yields an empty map,
// not an error.
func Load(path string) (map[string]Function, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]Function{}, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return map[string]Function{}, nil
	}
	var m map[string]Function
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if m == nil {
		m = map[string]Function{}
	}
	return m, nil
}

// Add creates or overwrites the function named name and saves it.
func Add(path, name string, f Function) error {
	m, err := Load(path)
	if err != nil {
		return err
	}
	m[name] = f
	return save(path, m)
}

// Remove deletes the function named name, erroring if it doesn't exist.
func Remove(path, name string) error {
	m, err := Load(path)
	if err != nil {
		return err
	}
	if _, ok := m[name]; !ok {
		return fmt.Errorf("no function named %q", name)
	}
	delete(m, name)
	return save(path, m)
}

// Names returns m's keys, sorted alphabetically.
func Names(m map[string]Function) []string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func save(path string, m map[string]Function) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal functions: %w", err)
	}
	return atomicfile.Write(path, data)
}
