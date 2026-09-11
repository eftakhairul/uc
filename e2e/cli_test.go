//go:build e2e && !windows

package e2e

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScriptLifecycle(t *testing.T) {
	env := sandbox(t)
	writeFile(t, "/tmp/killport.sh", "#!/bin/sh\n# @desc: kills a port\n# @usage: killport <port>\necho would kill port $1\n")

	out, code := runUC(t, env, "add", "/tmp/killport.sh", "killport")
	require.Equalf(t, 0, code, "add: output:\n%s", out)
	assert.Contains(t, out, "added", "add output")

	out, code = runUC(t, env, "list")
	require.Zero(t, code, "list output:\n%s", out)
	assert.Contains(t, out, "killport", "list output")
	assert.Contains(t, out, "kills a port", "list output")

	out, code = runUC(t, env, "which", "killport")
	require.Zero(t, code, "which output:\n%s", out)
	assert.Contains(t, out, "script", "which output")

	out, code = runUC(t, env, "killport", "8080")
	require.Zero(t, code, "run output:\n%s", out)
	assert.Contains(t, out, "would kill port 8080", "run output")

	out, code = runUC(t, env, "remove", "killport")
	require.Zero(t, code, "remove output:\n%s", out)
	assert.Contains(t, out, "removed", "remove output")

	_, code = runUC(t, env, "which", "killport")
	assert.Equalf(t, 127, code, "which after remove: want exit 127 (not found)")
}

func TestAliasLifecycle(t *testing.T) {
	env := sandbox(t)

	out, code := runUC(t, env, "alias", "add", "gs", "echo", "hi", "--desc", "greet")
	require.Equalf(t, 0, code, "alias add: output:\n%s", out)

	// Auto-append: the alias's own text has no $1/$@, yet invocation args
	// still land on the end (architecture §3.2b).
	out, code = runUC(t, env, "gs", "there")
	require.Zero(t, code, "run alias output:\n%s", out)
	assert.Equal(t, "hi there", strings.TrimSpace(out), "run alias output")

	out, code = runUC(t, env, "alias", "list")
	require.Zero(t, code, "alias list output:\n%s", out)
	assert.Contains(t, out, "gs", "alias list output")
	assert.Contains(t, out, "greet", "alias list output")

	editEnv := withEnv(env, "EDITOR=/usr/local/bin/fake-editor", "EDITOR_NEW_CONTENT=echo bye")
	out, code = runUC(t, editEnv, "alias", "edit", "gs")
	require.Zero(t, code, "alias edit output:\n%s", out)
	assert.Contains(t, out, "updated alias gs -> echo bye", "alias edit output")

	out, code = runUC(t, env, "gs", "now")
	require.Zero(t, code, "run edited alias output:\n%s", out)
	assert.Equal(t, "bye now", strings.TrimSpace(out), "run edited alias output")

	out, _ = runUC(t, env, "alias", "list")
	assert.Contains(t, out, "greet", "alias list after edit: desc preserved across edit")

	failEnv := withEnv(env, "EDITOR=/usr/local/bin/fake-editor-fail")
	out, code = runUC(t, failEnv, "alias", "edit", "gs")
	assert.NotZero(t, code, "alias edit with failing editor: want nonzero exit")
	assert.Contains(t, out, "nothing saved", "alias edit failure output")

	out, code = runUC(t, env, "gs", "still")
	require.Zero(t, code, "alias after aborted edit output:\n%s", out)
	assert.Equal(t, "bye still", strings.TrimSpace(out), "alias after aborted edit output")

	out, code = runUC(t, env, "alias", "remove", "gs")
	require.Zero(t, code, "alias remove output:\n%s", out)
	assert.Contains(t, out, "removed", "alias remove output")
}

func TestFunctionLifecycle(t *testing.T) {
	env := sandbox(t)

	_, code := runUC(t, env, "function", "add", "build", "echo building", "--desc", "build step")
	require.Zero(t, code, "function add build")

	_, code = runUC(t, env, "function", "add", "ship", "uc build && echo shipped")
	require.Zero(t, code, "function add ship")

	out, code := runUC(t, env, "ship")
	require.Equalf(t, 0, code, "run ship: output:\n%s", out)
	assert.Contains(t, out, "building", "ship output")
	assert.Contains(t, out, "shipped", "ship output")

	out, code = runUC(t, env, "function", "list")
	require.Zero(t, code, "function list output:\n%s", out)
	assert.Contains(t, out, "build step", "function list output")

	editEnv := withEnv(env, "EDITOR=/usr/local/bin/fake-editor", "EDITOR_NEW_CONTENT=echo building v2")
	out, code = runUC(t, editEnv, "function", "edit", "build")
	require.Zero(t, code, "function edit output:\n%s", out)
	assert.Contains(t, out, "updated function build", "function edit output")

	out, code = runUC(t, env, "ship")
	require.Zero(t, code, "run ship after edit output:\n%s", out)
	assert.Contains(t, out, "building v2", "run ship after edit output")

	out, code = runUC(t, env, "function", "remove", "ship")
	require.Zero(t, code, "function remove output:\n%s", out)
	assert.Contains(t, out, "removed", "function remove output")
}

func TestHistoryAndReplay(t *testing.T) {
	env := sandbox(t)

	_, code := runUC(t, env, "alias", "add", "gs", "echo original")
	require.Zero(t, code, "alias add")

	_, code = runUC(t, env, "gs")
	require.Zero(t, code, "run gs")

	out, code := runUC(t, env, "history")
	require.Equalf(t, 0, code, "history: output:\n%s", out)
	assert.Contains(t, out, "gs", "history output")
	assert.Contains(t, out, "alias", "history output")

	// Redefine the alias, then replay entry #1 (the "gs" run above) — it
	// should pick up the new definition, not a snapshot taken at record
	// time (architecture: `uc history run` re-resolves fresh).
	editEnv := withEnv(env, "EDITOR=/usr/local/bin/fake-editor", "EDITOR_NEW_CONTENT=echo redefined")
	_, code = runUC(t, editEnv, "alias", "edit", "gs")
	require.Zero(t, code, "alias edit")

	out, code = runUC(t, env, "history", "run", "1")
	require.Equalf(t, 0, code, "history run 1: output:\n%s", out)
	assert.Contains(t, out, "redefined", "history run 1 output")
}

func TestReservedNameRejection(t *testing.T) {
	env := sandbox(t)

	out, code := runUC(t, env, "alias", "add", "list", "echo x")
	assert.Equalf(t, 1, code, "alias add list: output:\n%s", out)
	assert.Contains(t, out, "reserved", "alias add list output")

	out, code = runUC(t, env, "function", "add", "history", "echo x")
	assert.Equalf(t, 1, code, "function add history: output:\n%s", out)
	assert.Contains(t, out, "reserved", "function add history output")

	// Reserved-name checks are on the bare name (extension stripped), not
	// the literal typed name — "history.sh" collides with "history" once
	// its .sh extension is stripped (architecture §3.4).
	writeFile(t, "/tmp/backup.sh", "#!/bin/sh\necho hi\n")
	out, code = runUC(t, env, "add", "/tmp/backup.sh", "history.sh")
	assert.Equalf(t, 1, code, "add history.sh: output:\n%s", out)
	assert.Contains(t, out, "reserved", "add history.sh output")
}

func TestShadowWarning(t *testing.T) {
	env := sandbox(t)

	writeFile(t, "/tmp/deploy.sh", "#!/bin/sh\necho script deploy\n")
	_, code := runUC(t, env, "add", "/tmp/deploy.sh", "deploy")
	require.Zero(t, code, "add deploy script")

	out, code := runUC(t, env, "alias", "add", "deploy", "echo alias deploy")
	require.Equalf(t, 0, code, "alias add deploy: output:\n%s", out)

	assert.Contains(t, out, "note:", "alias add deploy output")
	assert.Contains(t, out, "shadowed", "alias add deploy output")

	// Script precedence wins despite the alias existing under the same name.
	out, code = runUC(t, env, "deploy")
	require.Zero(t, code, "run deploy output:\n%s", out)
	assert.Contains(t, out, "script deploy", "run deploy output")

	out, code = runUC(t, env, "which", "deploy")
	require.Zero(t, code, "which deploy output:\n%s", out)
	assert.Contains(t, out, "script", "which deploy output")
}

// withEnv returns a copy of base with extra KEY=VALUE entries appended,
// without mutating base (sandbox's env slice is reused across calls
// within a test).
func withEnv(base []string, extra ...string) []string {
	out := make([]string, 0, len(base)+len(extra))
	out = append(out, base...)
	out = append(out, extra...)
	return out
}
