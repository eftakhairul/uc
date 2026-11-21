package history

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// history_size 0 means off: Append must not grow the file (improvement
// spec §4.6), and any cap must truncate.
func TestAppendHistoryOffWritesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	for _, cap := range []int{0, -1} {
		if err := Append(path, Entry{Name: "x", Timestamp: time.Now()}, cap); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("history.json written with history off: %v", err)
	}
}

func TestAppendAlwaysTruncates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	for i := range 10 {
		if err := Append(path, Entry{Name: fmt.Sprint("n", i), Timestamp: time.Now()}, 3); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 || entries[0].Name != "n7" || entries[2].Name != "n9" {
		t.Errorf("entries = %+v, want the newest three", entries)
	}
}

func TestClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	if err := Clear(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("Clear created a missing history file")
	}
	_ = Append(path, Entry{Name: "deploy", Args: []string{"--token=abc"}, Timestamp: time.Now()}, 25)
	if err := Clear(path); err != nil {
		t.Fatal(err)
	}
	entries, err := Load(path)
	if err != nil || len(entries) != 0 {
		t.Errorf("after Clear: %v, %v", entries, err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "abc") {
		t.Error("cleared history still contains the old arguments")
	}
}

func TestLoadCorruptMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	if err := os.WriteFile(path, []byte("[{"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "cannot read history.json: invalid JSON at line 1 — delete it, or run 'uc history clear'") {
		t.Errorf("err = %v", err)
	}
}
