package executor

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The success paths of Run/RunAlias/RunFunction replace (or on Windows,
// exit) the current process, so in-process tests can only cover the error
// paths before exec1; the success paths are covered by the e2e suites.

// emptyPath points PATH at an empty directory so no interpreter or bash
// can be found.
func emptyPath(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func TestRunInterpreterNotFound(t *testing.T) {
	emptyPath(t)
	err := Run(filepath.Join(t.TempDir(), "script.sh"), nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "interpreter for \".sh\" not found") {
		t.Errorf("error = %q, want interpreter-not-found", err)
	}
}

// stubInterpreter creates an executable stub named name (plus the .bat
// extension Windows needs for LookPath) in dir.
func stubInterpreter(t *testing.T, dir, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name += ".bat"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLookupInterpreterPrefersFirstCandidate(t *testing.T) {
	dir := t.TempDir()
	stubInterpreter(t, dir, "python3")
	stubInterpreter(t, dir, "python")
	t.Setenv("PATH", dir)

	_, name, err := lookupInterpreter(".py")
	if err != nil {
		t.Fatalf("lookupInterpreter: %v", err)
	}
	if name != "python3" {
		t.Errorf("name = %q, want python3 (first candidate wins)", name)
	}
}

// The bug this guards: .py scripts were dead wherever only "python"
// exists (Windows, some minimal images).
func TestLookupInterpreterFallsBackToPython(t *testing.T) {
	dir := t.TempDir()
	stubInterpreter(t, dir, "python")
	t.Setenv("PATH", dir)

	bin, name, err := lookupInterpreter(".py")
	if err != nil {
		t.Fatalf("lookupInterpreter: %v", err)
	}
	if name != "python" {
		t.Errorf("name = %q, want python", name)
	}
	if !strings.Contains(bin, "python") {
		t.Errorf("bin = %q, want the stub path", bin)
	}
}

func TestLookupInterpreterNoneFound(t *testing.T) {
	emptyPath(t)
	_, _, err := lookupInterpreter(".py")
	if err == nil {
		t.Fatal("want error, got nil")
	}
	for _, want := range []string{"python3", "python"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to list %q", err, want)
		}
	}
}

func TestLookupInterpreterUnknownExtension(t *testing.T) {
	_, _, err := lookupInterpreter(".unknown")
	if !errors.Is(err, errNoInterpreter) {
		t.Errorf("error = %v, want errNoInterpreter", err)
	}
}

func TestRunExtensionlessNotExecutable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plain")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Run(path, nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "is not executable") {
		t.Errorf("error = %q, want not-executable", err)
	}
}

func TestRunAliasBashNotFound(t *testing.T) {
	emptyPath(t)
	err := RunAlias("echo hi", "gs", nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "bash not found on PATH") {
		t.Errorf("error = %q, want bash-not-found", err)
	}
}

func TestRunFunctionBashNotFound(t *testing.T) {
	emptyPath(t)
	err := RunFunction("echo hi", "greet", nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "bash not found on PATH") {
		t.Errorf("error = %q, want bash-not-found", err)
	}
}

func TestExecReplaceNotFound(t *testing.T) {
	emptyPath(t)
	err := ExecReplace([]string{"no-such-binary"})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("error = %q, want not-found", err)
	}
}
