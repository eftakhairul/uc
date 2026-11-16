// Package history reads and appends to history.json — the last N
// invocations (architecture §8), capped and truncated oldest-first.
package history

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/eftakhairul/uc/internal/atomicfile"
	"github.com/eftakhairul/uc/internal/jsonerr"
)

// Entry is one recorded invocation.
type Entry struct {
	Name      string    `json:"name"`
	Args      []string  `json:"args"`
	Timestamp time.Time `json:"timestamp"`
	Kind      string    `json:"kind"` // "script", "alias", or "function" (architecture §8.2)
}

// Load reads all entries from path, oldest first. A missing file yields
// an empty slice, not an error.
func Load(path string) ([]Entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read %s: %w", filepath.Base(path), err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("cannot read %s: %s — delete it, or run 'uc history clear'", filepath.Base(path), jsonerr.Describe(data, err))
	}
	return entries, nil
}

// Append records a new invocation, truncating to the newest cap entries
// (oldest dropped first). cap <= 0 means history is off: nothing is
// written. Written atomically under a lock (architecture §8.2) so racing
// uc processes neither corrupt the file nor lose each other's entry.
func Append(path string, entry Entry, cap int) error {
	if cap <= 0 {
		return nil
	}
	return atomicfile.WithLock(path, func() error {
		entries, err := Load(path)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
		if len(entries) > cap {
			entries = entries[len(entries)-cap:]
		}
		return save(path, entries)
	})
}

// Clear empties the history file. A missing file stays missing.
func Clear(path string) error {
	return atomicfile.WithLock(path, func() error {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return nil
		}
		return save(path, []Entry{})
	})
}

func save(path string, entries []Entry) error {
	if entries == nil {
		entries = []Entry{}
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode history: %w", err)
	}
	return atomicfile.Write(path, data)
}
