//go:build e2e && !windows

package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitIdentity gives a sandbox a git author, since the container has no
// global git config and sync commits would otherwise fail.
func gitIdentity(env []string) []string {
	return withEnv(env,
		"GIT_AUTHOR_NAME=uc e2e", "GIT_AUTHOR_EMAIL=e2e@example.com",
		"GIT_COMMITTER_NAME=uc e2e", "GIT_COMMITTER_EMAIL=e2e@example.com")
}

// Two machines sharing one bare repo: state set up on A appears on B via
// init, a removal on A propagates to B via push/pull, and B's unpushed
// changes are protected from a plain pull.
func TestSyncAcrossTwoMachines(t *testing.T) {
	root := sandboxRoot(t)
	envA := gitIdentity(sandboxAt(t, root+"/a"))
	envB := gitIdentity(sandboxAt(t, root+"/b"))
	remote := root + "/remote.git"
	out, code := execIn(t, nil, "git", "init", "-q", "--bare", remote)
	require.Zerof(t, code, "git init --bare: output:\n%s", out)

	writeFile(t, root+"/killport.sh", "#!/bin/sh\n# @desc: kills a port\necho would kill port $1\n")
	out, code = runUC(t, envA, "add", root+"/killport.sh", "killport")
	require.Zerof(t, code, "A add: output:\n%s", out)
	out, code = runUC(t, envA, "alias", "add", "hi", "echo", "hello")
	require.Zerof(t, code, "A alias add: output:\n%s", out)

	out, code = runUC(t, envA, "sync", "init", remote)
	require.Zerof(t, code, "A sync init: output:\n%s", out)
	assert.Contains(t, out, "pushed initial state", "A sync init output")
	assert.Contains(t, out, "+ scripts/killport", "A sync init output")
	assert.Contains(t, out, "+ aliases.json", "A sync init output")

	out, code = runUC(t, envB, "sync", "init", remote)
	require.Zerof(t, code, "B sync init: output:\n%s", out)
	assert.Contains(t, out, "pulled existing state", "B sync init output")

	out, code = runUC(t, envB, "killport", "8080")
	require.Zerof(t, code, "B run synced script: output:\n%s", out)
	assert.Contains(t, out, "would kill port 8080", "B run synced script")
	out, code = runUC(t, envB, "hi")
	require.Zerof(t, code, "B run synced alias: output:\n%s", out)
	assert.Contains(t, out, "hello", "B run synced alias")

	out, code = runUC(t, envA, "remove", "killport")
	require.Zerof(t, code, "A remove: output:\n%s", out)
	out, code = runUC(t, envA, "sync", "push")
	require.Zerof(t, code, "A sync push: output:\n%s", out)
	assert.Contains(t, out, "- scripts/killport", "A sync push output")

	out, code = runUC(t, envB, "sync", "status")
	require.Zerof(t, code, "B sync status: output:\n%s", out)
	assert.Contains(t, out, "remote: "+remote, "B sync status output")
	assert.Contains(t, out, "1 to pull", "B sync status output")

	out, code = runUC(t, envB, "sync", "pull")
	require.Zerof(t, code, "B sync pull: output:\n%s", out)
	assert.Contains(t, out, "- scripts/killport", "B sync pull output")
	_, code = runUC(t, envB, "killport")
	assert.Equal(t, 127, code, "B run removed script: want exit 127 (not found)")

	// An unpushed local change on B blocks a plain pull rather than being
	// silently discarded.
	out, code = runUC(t, envB, "alias", "add", "local-only", "true")
	require.Zerof(t, code, "B alias add: output:\n%s", out)
	out, code = runUC(t, envB, "sync", "pull")
	assert.NotZero(t, code, "B pull with unpushed changes: want nonzero exit")
	assert.Contains(t, out, "local changes not yet pushed", "B refused pull output")
	_, code = runUC(t, envB, "which", "local-only")
	assert.Zero(t, code, "B's unpushed alias must survive a refused pull")
}

func TestSyncWithoutInit(t *testing.T) {
	env := sandbox(t)
	out, code := runUC(t, env, "sync", "push")
	assert.NotZero(t, code, "sync push before init: want nonzero exit")
	assert.Contains(t, out, "uc sync init", "sync push before init output")

	out, code = runUC(t, env, "sync")
	assert.NotZero(t, code, "bare sync: want nonzero exit")
	assert.Contains(t, out, "usage: uc sync", "bare sync output")

	out, code = runUC(t, env, "help", "sync")
	require.Zerof(t, code, "help sync: output:\n%s", out)
	assert.Contains(t, out, "uc sync init <remote-url>", "help sync output")
}
