// Package history reads and appends to history.json — the last N
// invocations (architecture §8), capped and truncated oldest-first.
package history

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/eftakhairul/uc/internal/atomicfile"
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
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return entries, nil
}

// Append records a new invocation, truncating to the newest cap entries
// (oldest dropped first). Written atomically (architecture §8.2) to avoid
// corrupting the file on a racing concurrent write.
func Append(path string, entry Entry, cap int) error {
	entries, err := Load(path)
	if err != nil {
		return err
	}
	entries = append(entries, entry)
	if cap > 0 && len(entries) > cap {
		entries = entries[len(entries)-cap:]
	}
	if entries == nil {
		entries = []Entry{}
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal history: %w", err)
	}
	return atomicfile.Write(path, data)
}
