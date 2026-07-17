package history

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAppendAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")

	e1 := Entry{Name: "killport", Args: []string{"8080"}, Timestamp: time.Now().UTC()}
	if err := Append(path, e1, 25); err != nil {
		t.Fatalf("Append: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "killport" {
		t.Fatalf("got %+v", loaded)
	}
}

func TestAppend_CapTruncatesOldest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")

	for i := range 5 {
		e := Entry{Name: "script", Args: []string{string(rune('a' + i))}, Timestamp: time.Now().UTC()}
		if err := Append(path, e, 3); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 3 {
		t.Fatalf("got %d entries, want 3", len(loaded))
	}
	// oldest-first; should contain the last 3 appends: c, d, e
	want := []string{"c", "d", "e"}
	for i, e := range loaded {
		if e.Args[0] != want[i] {
			t.Errorf("loaded[%d].Args[0] = %q, want %q", i, e.Args[0], want[i])
		}
	}
}

func TestLoad_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded != nil {
		t.Errorf("got %+v, want nil", loaded)
	}
}
