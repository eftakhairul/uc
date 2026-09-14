//go:build e2e && !windows

package e2e

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcexec "github.com/testcontainers/testcontainers-go/exec"
	"github.com/testcontainers/testcontainers-go/wait"
)

const containerBinPath = "/usr/local/bin/uc"

var container testcontainers.Container

func TestMain(m *testing.M) {
	os.Exit(runMain(m))
}

func runMain(m *testing.M) int {
	ctx := context.Background()

	artifactsDir, err := buildArtifacts()
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: %v\n", err)
		return 1
	}
	defer os.RemoveAll(artifactsDir)

	req := testcontainers.ContainerRequest{
		Image: "debian:bookworm-slim",
		Cmd:   []string{"sleep", "infinity"},
		Files: []testcontainers.ContainerFile{
			{HostFilePath: filepath.Join(artifactsDir, "uc"), ContainerFilePath: containerBinPath, FileMode: 0o755},
			{HostFilePath: filepath.Join(artifactsDir, "fake-editor"), ContainerFilePath: "/usr/local/bin/fake-editor", FileMode: 0o755},
			{HostFilePath: filepath.Join(artifactsDir, "fake-editor-fail"), ContainerFilePath: "/usr/local/bin/fake-editor-fail", FileMode: 0o755},
		},
		WaitingFor: wait.ForExec([]string{containerBinPath, "version"}),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: start container: %v\n", err)
		return 1
	}
	defer func() {
		if err := c.Terminate(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "e2e: terminate container: %v\n", err)
		}
	}()
	container = c

	return m.Run()
}

// buildArtifacts cross-compiles uc for linux/amd64 and writes the fake
// editor scripts used by the alias/function edit tests, all into one temp
// dir ready to be copied into the container.
func buildArtifacts() (string, error) {
	dir, err := os.MkdirTemp("", "uc-e2e-artifacts")
	if err != nil {
		return "", err
	}

	cmd := exec.Command("go", "build", "-o", filepath.Join(dir, "uc"), "github.com/eftakhairul/uc/cmd/uc")
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if out, buildErr := cmd.CombinedOutput(); buildErr != nil {
		os.RemoveAll(dir)
		return "", fmt.Errorf("build linux uc binary: %w\n%s", buildErr, out)
	}

	// The fake editor writes $EDITOR_NEW_CONTENT to whatever path it's
	// invoked with — a stand-in for $EDITOR in the alias/function edit
	// round-trip tests, controlled per-test via env rather than per-test
	// script content.
	editor := "#!/bin/sh\nprintf '%s' \"$EDITOR_NEW_CONTENT\" > \"$1\"\n"
	editorFail := "#!/bin/sh\nprintf 'should not be saved' > \"$1\"\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "fake-editor"), []byte(editor), 0o755); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "fake-editor-fail"), []byte(editorFail), 0o755); err != nil {
		os.RemoveAll(dir)
		return "", err
	}

	return dir, nil
}

// sandbox creates a fresh, isolated $UC_HOME/$XDG_CONFIG_HOME under /tmp
// inside the shared container for one test, and returns the env slice
// (KEY=VALUE) to pass to every uc invocation in that test — so tests never
// see each other's registered scripts/aliases/functions/history despite
// sharing one container.
func sandbox(t *testing.T) []string {
	t.Helper()
	root := "/tmp/uc-e2e/" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	_, code := execIn(t, nil, "mkdir", "-p", root)
	require.Zerof(t, code, "mkdir sandbox %s: nonzero exit", root)
	return []string{
		"HOME=" + root,
		"UC_HOME=" + root + "/uc-home",
		"XDG_CONFIG_HOME=" + root + "/config",
		"PATH=/usr/local/bin:/usr/bin:/bin",
	}
}

// writeFile creates a file with the given content at path inside the
// container — used to stage script source files for `uc add`. content is
// passed as an argv element (sh's $1), not interpolated into shell syntax,
// so it needs no escaping regardless of what it contains.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	_, code := execIn(t, nil, "sh", "-c", `printf '%s' "$1" > "$2"`, "sh", content, path)
	require.Zerof(t, code, "write %s: nonzero exit", path)
}

// execIn runs argv inside the shared container with the given env
// (KEY=VALUE strings, nil for the container's defaults) and returns
// combined stdout+stderr and the exit code.
func execIn(t *testing.T, env []string, argv ...string) (string, int) {
	t.Helper()
	// Multiplexed strips Docker's exec stream-framing headers, which
	// otherwise leak into the output as raw bytes since no TTY is
	// allocated (stdout/stderr share one multiplexed stream).
	opts := []tcexec.ProcessOption{tcexec.Multiplexed()}
	if env != nil {
		opts = append(opts, tcexec.WithEnv(env))
	}
	code, reader, err := container.Exec(context.Background(), argv, opts...)
	if err != nil {
		t.Fatalf("exec %v: %v", argv, err)
	}
	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read output of %v: %v", argv, err)
	}
	return string(out), code
}

// runUC runs the uc binary inside the container with env, returning
// combined stdout+stderr and the exit code.
func runUC(t *testing.T, env []string, args ...string) (string, int) {
	t.Helper()
	return execIn(t, env, append([]string{containerBinPath}, args...)...)
}
