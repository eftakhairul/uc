// Package atomicfile provides write-to-temp-then-rename writes, shared by
// history.json and commands.json (architecture §8.2) to reduce corruption
// risk from a racing concurrent writer, plus an advisory lock for their
// read-modify-write cycles.
package atomicfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// DirMode is the mode for every directory uc creates: scripts and
// commands often hold tokens and internal hostnames, so they are private
// to the user.
const DirMode = 0o700

// Write atomically replaces path's contents with data, creating path's
// parent directory if needed. The file is 0o600.
func Write(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, DirMode); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once renamed

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, path, err)
	}
	return nil
}

// WithLock runs fn while holding an exclusive advisory lock on
// path + ".lock", so two uc processes doing load-modify-write on the same
// file can't lose each other's update. The lock file is created (with its
// parent directory) if needed and deliberately left in place afterwards:
// deleting it would let a third process lock a different inode.
func WithLock(path string, fn func() error) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, DirMode); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	defer f.Close()
	if err := lock(f); err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer unlock(f)
	return fn()
}
