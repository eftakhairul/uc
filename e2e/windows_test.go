//go:build e2e && windows

package e2e

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Windows e2e: the unix suite drives uc inside a Linux container, which
// can't exercise the Windows exec fallback (exec_windows.go). This suite
// builds uc.exe and runs it natively on a Windows host instead. Scripts,
// aliases, and functions need bash on PATH (Git Bash) — present on GitHub's
// windows runners; bash-dependent tests skip cleanly when it's missing.

var winBinPath string

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	dir, err := os.MkdirTemp("", "uc-e2e-win")
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)

	winBinPath = filepath.Join(dir, "uc.exe")
	cmd := exec.Command("go", "build", "-o", winBinPath, "github.com/eftakhairul/uc/cmd/uc")
	if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
		fmt.Fprintf(os.Stderr, "e2e: build uc.exe: %v\n%s\n", buildErr, out)
		return 1
	}

	return m.Run()
}

// sandbox returns env overrides isolating $UC_HOME/$XDG_CONFIG_HOME in temp
// dirs, appended to the host environment (os/exec keeps the last duplicate,
// so the overrides win).
func sandbox(t *testing.T) []string {
	t.Helper()
	root := t.TempDir()
	return append(os.Environ(),
		"UC_HOME="+filepath.Join(root, "uc-home"),
		"XDG_CONFIG_HOME="+filepath.Join(root, "config"),
	)
}

// runUC runs the built uc.exe with env, returning combined stdout+stderr
// and the exit code.
func runUC(t *testing.T, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(winBinPath, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return string(out), exitErr.ExitCode()
		}
		t.Fatalf("run uc %v: %v", args, err)
	}
	return string(out), 0
}

func requireBash(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not on PATH — scripts/aliases/functions need Git Bash on Windows")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func TestVersion(t *testing.T) {
	out, code := runUC(t, sandbox(t), "version")
	require.Zerof(t, code, "version: output:\n%s", out)
	assert.NotEmpty(t, strings.TrimSpace(out), "version output")
}

func TestScriptLifecycle(t *testing.T) {
	requireBash(t)
	env := sandbox(t)
	// Registered under its .sh basename: extensionless files aren't
	// executable on Windows, so the extension is what routes the script to
	// bash. It's still invoked by the bare name.
	src := filepath.Join(t.TempDir(), "killport.sh")
	writeFile(t, src, "#!/bin/bash\n# @desc: kills a port\necho would kill port $1\n")

	out, code := runUC(t, env, "add", src)
	require.Equalf(t, 0, code, "add: output:\n%s", out)
	assert.Contains(t, out, "added", "add output")

	out, code = runUC(t, env, "list")
	require.Zerof(t, code, "list output:\n%s", out)
	assert.Contains(t, out, "killport", "list output")
	assert.Contains(t, out, "kills a port", "list output")

	// The core of the Windows fix: uc must actually run the script
	// (spawn-and-wait) instead of failing with EWINDOWS.
	out, code = runUC(t, env, "killport", "8080")
	require.Zerof(t, code, "run output:\n%s", out)
	assert.Contains(t, out, "would kill port 8080", "run output")

	out, code = runUC(t, env, "remove", "killport")
	require.Zerof(t, code, "remove output:\n%s", out)

	_, code = runUC(t, env, "which", "killport")
	assert.Equal(t, 127, code, "which after remove: want exit 127 (not found)")
}

func TestScriptExitCodeMirrored(t *testing.T) {
	requireBash(t)
	env := sandbox(t)
	src := filepath.Join(t.TempDir(), "fail.sh")
	writeFile(t, src, "#!/bin/bash\nexit 7\n")

	out, code := runUC(t, env, "add", src)
	require.Equalf(t, 0, code, "add: output:\n%s", out)

	_, code = runUC(t, env, "fail")
	assert.Equal(t, 7, code, "uc must mirror the script's exit code")
}

func TestAliasLifecycle(t *testing.T) {
	requireBash(t)
	env := sandbox(t)

	out, code := runUC(t, env, "alias", "add", "gs", "echo", "hi", "--desc", "greet")
	require.Equalf(t, 0, code, "alias add: output:\n%s", out)

	// Auto-append: invocation args land on the end of the alias command.
	out, code = runUC(t, env, "gs", "there")
	require.Zerof(t, code, "run alias output:\n%s", out)
	assert.Equal(t, "hi there", strings.TrimSpace(out), "run alias output")

	_, code = runUC(t, env, "alias", "remove", "gs")
	assert.Zero(t, code, "alias remove")
}

func TestFunctionLifecycle(t *testing.T) {
	requireBash(t)
	env := sandbox(t)

	out, code := runUC(t, env, "function", "add", "greet", "echo hello $1")
	require.Equalf(t, 0, code, "function add: output:\n%s", out)

	out, code = runUC(t, env, "greet", "world")
	require.Zerof(t, code, "run function output:\n%s", out)
	assert.Equal(t, "hello world", strings.TrimSpace(out), "run function output")
}

func TestHistoryRecorded(t *testing.T) {
	requireBash(t)
	env := sandbox(t)

	out, code := runUC(t, env, "alias", "add", "hi", "echo", "hi")
	require.Equalf(t, 0, code, "alias add: output:\n%s", out)
	_, code = runUC(t, env, "hi")
	require.Zero(t, code, "run alias")

	out, code = runUC(t, env, "history")
	require.Zerof(t, code, "history output:\n%s", out)
	assert.Contains(t, out, "hi", "history output")
	assert.Contains(t, out, "alias", "history output")
}
