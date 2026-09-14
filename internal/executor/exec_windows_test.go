//go:build windows

package executor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runChild is the testable half of the Windows exec1 fallback: it must
// mirror the child's exit code without exiting the test process.

func lookCmd(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("cmd")
	if err != nil {
		t.Fatalf("cmd.exe not on PATH: %v", err)
	}
	return bin
}

func TestRunChildExitCodeZero(t *testing.T) {
	bin := lookCmd(t)
	code, err := runChild(bin, []string{"cmd", "/c", "exit 0"}, os.Environ())
	if err != nil {
		t.Fatalf("runChild: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestRunChildExitCodeMirrored(t *testing.T) {
	bin := lookCmd(t)
	code, err := runChild(bin, []string{"cmd", "/c", "exit 7"}, os.Environ())
	if err != nil {
		t.Fatalf("runChild: %v", err)
	}
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
}

func TestRunChildEnvPassedThrough(t *testing.T) {
	bin := lookCmd(t)
	out := filepath.Join(t.TempDir(), "out.txt")
	env := append(os.Environ(), "UC_TEST_MARKER=hello-from-uc")
	code, err := runChild(bin, []string{"cmd", "/c", "echo %UC_TEST_MARKER%> " + out}, env)
	if err != nil || code != 0 {
		t.Fatalf("runChild: code=%d err=%v", code, err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.Contains(got, "hello-from-uc") {
		t.Errorf("child env output = %q, want it to contain the marker", got)
	}
}

func TestRunChildStartFailure(t *testing.T) {
	_, err := runChild(filepath.Join(t.TempDir(), "missing.exe"), []string{"missing"}, os.Environ())
	if err == nil {
		t.Fatal("want error for missing binary, got nil")
	}
}
