package executor

import (
	"os"
	"path/filepath"
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
	if !strings.Contains(err.Error(), `interpreter "bash" not found`) {
		t.Errorf("error = %q, want interpreter-not-found", err)
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
