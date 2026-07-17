package functions

import (
	"path/filepath"
	"testing"
)

func TestAdd_LoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "functions.json")
	desc := "Full ship pipeline"

	if err := Add(path, "shiplt", Function{Body: "uc build && uc test && uc deploy", Desc: &desc}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	f, ok := m["shiplt"]
	if !ok {
		t.Fatalf("function %q not found", "shiplt")
	}
	if f.Body != "uc build && uc test && uc deploy" {
		t.Errorf("Body = %q", f.Body)
	}
	if f.Desc == nil || *f.Desc != desc {
		t.Errorf("Desc = %v, want %q", f.Desc, desc)
	}
}

func TestRemove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "functions.json")
	if err := Add(path, "shiplt", Function{Body: "uc build"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := Remove(path, "shiplt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := m["shiplt"]; ok {
		t.Errorf("function %q still present after Remove", "shiplt")
	}
}

func TestRemove_NotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "functions.json")
	if err := Remove(path, "nope"); err == nil {
		t.Fatal("expected error removing a nonexistent function")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	m, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("got %+v, want empty", m)
	}
}
