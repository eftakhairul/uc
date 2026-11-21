//go:build e2e && !windows

package e2e

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sandboxPath returns a path variable (UC_HOME, XDG_CONFIG_HOME, HOME) from
// a sandbox env.
func sandboxPath(env []string, key string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, key+"="); ok {
			return v
		}
	}
	return ""
}

// fileExists reports whether path exists inside the container.
func fileExists(t *testing.T, path string) bool {
	t.Helper()
	_, code := execIn(t, nil, "test", "-e", path)
	return code == 0
}

// improvement spec §1: bare uc with nothing registered shows help.
func TestEmptyRegistryShowsHelp(t *testing.T) {
	env := sandbox(t)
	out, code := runUC(t, env)
	require.Zerof(t, code, "bare uc: output:\n%s", out)
	assert.True(t, strings.HasPrefix(out, "usage: uc <name> [<args>]"), "bare uc output:\n%s", out)

	help, code := runUC(t, env, "help")
	require.Zero(t, code)
	assert.Equal(t, help, out, "bare uc must print exactly uc help")

	out, code = runUC(t, env, "list")
	require.Zero(t, code)
	assert.Equal(t, "Nothing registered yet — see 'uc help' to get started\n", out)
}

// improvement spec §2.
func TestGitStyleHelp(t *testing.T) {
	env := sandbox(t)
	overview, code := runUC(t, env, "help")
	require.Zero(t, code)
	for _, want := range []string{"create and change scripts", "find and inspect", "aliases and functions", "sync and setup",
		"   alias        Add, remove, list, or edit aliases to shell commands", "   export ", "   import ",
		"See 'uc help <command>'"} {
		assert.Contains(t, overview, want)
	}
	assert.NotContains(t, overview, "uc alias remove", "verbs belong in uc help alias")
	for _, line := range strings.Split(overview, "\n") {
		assert.LessOrEqualf(t, len([]rune(line)), 80, "line too long: %q", line)
	}

	for _, args := range [][]string{{"-h"}, {"--help"}} {
		out, code := runUC(t, env, args...)
		assert.Zero(t, code)
		assert.Equal(t, overview, out, "uc %v", args)
	}

	helpAlias, code := runUC(t, env, "help", "alias")
	require.Zero(t, code)
	assert.True(t, strings.HasPrefix(helpAlias, "usage: uc alias add <name> [--desc <text>] [--] <command>...\n   or: uc alias remove <name>\n   or: uc alias list\n   or: uc alias edit <name>\n\n"), helpAlias)
	out, _ := runUC(t, env, "alias", "--help")
	assert.Equal(t, helpAlias, out)

	version, _ := runUC(t, env, "version")
	for _, args := range [][]string{{"-v"}, {"--version"}} {
		out, code := runUC(t, env, args...)
		assert.Zero(t, code)
		assert.Equal(t, version, out)
	}
}

// improvement spec §3: usage errors are friendly, exit 2.
func TestFriendlyUsageErrors(t *testing.T) {
	env := sandbox(t)
	out, code := runUC(t, env, "history", "-n")
	assert.Equal(t, 2, code)
	assert.Equal(t, "uc: 'history' expects a number, got '-n'\n"+
		"usage: uc history [<n>]\n   or: uc history run <n>\n   or: uc history clear\n"+
		"See 'uc help history'.\n", out)

	for _, args := range [][]string{
		{"list", "junk"}, {"--foo"}, {"remove", "--all"}, {"function", "add", "f", "echo", "hi"},
		{"history", "run", "x"}, {"completion", "pwsh"}, {"alias", "add", "x", "--desc"},
	} {
		out, code := runUC(t, env, args...)
		assert.Equalf(t, 2, code, "uc %v:\n%s", args, out)
		assert.Contains(t, out, "See 'uc help", "uc %v", args)
		for _, bad := range []string{"strconv", "invalid syntax", "exit status", "json: cannot"} {
			assert.NotContains(t, out, bad, "uc %v", args)
		}
	}
	// Nothing was registered by any of them.
	out, _ = runUC(t, env, "list")
	assert.Contains(t, out, "Nothing registered yet")
}

func TestNotFoundSuggestsAndExitCodes(t *testing.T) {
	env := sandbox(t)
	_, code := runUC(t, env, "alias", "add", "killport", "echo", "kill")
	require.Zero(t, code)

	out, code := runUC(t, env, "kilport")
	assert.Equal(t, 127, code)
	assert.Equal(t, "uc: 'kilport' is not a uc command or a registered name.\n\nThe most similar name is\n\tkillport\n", out)

	out, code = runUC(t, env, "help", "kilport")
	assert.Equal(t, 1, code, "help on an unknown name exits 1, not 127")
	assert.Contains(t, out, "The most similar name is")

	out, code = runUC(t, env, "xyzzy")
	assert.Equal(t, 127, code)
	assert.Equal(t, "uc: 'xyzzy' is not a uc command or a registered name — see 'uc list'\n", out)
}

// improvement spec §3.6: a broken settings.json warns and never blocks.
func TestBrokenSettingsDoNotBlock(t *testing.T) {
	env := sandbox(t)
	cfg := sandboxPath(env, "XDG_CONFIG_HOME") + "/uc"
	_, code := execIn(t, nil, "mkdir", "-p", cfg)
	require.Zero(t, code)
	writeFile(t, cfg+"/settings.json", `{"history_size": "x"}`)

	out, code := runUC(t, env, "help")
	require.Zerof(t, code, "help with broken settings:\n%s", out)
	assert.Contains(t, out, "uc: warning: ignoring settings.json: \"history_size\" must be a whole number — using defaults")
	assert.Contains(t, out, "usage: uc <name>")

	out, code = runUC(t, env, "__complete", "l")
	require.Zero(t, code)
	assert.NotContains(t, out, "warning", "completion must stay silent")
}

// improvement spec §4.1: names never escape the scripts directory.
func TestTraversalNamesNeverEscape(t *testing.T) {
	env := sandbox(t)
	home := sandboxPath(env, "UC_HOME")
	_, code := execIn(t, nil, "mkdir", "-p", home+"/scripts")
	require.Zero(t, code)
	writeFile(t, home+"/victim", "keep me")
	writeFile(t, home+"/out", "#!/bin/sh\necho OUTSIDE-RAN\n")
	_, code = execIn(t, nil, "chmod", "+x", home+"/out")
	require.Zero(t, code)

	out, code := runUC(t, env, "remove", "../victim")
	assert.Equalf(t, 127, code, "remove ../victim:\n%s", out)
	assert.True(t, fileExists(t, home+"/victim"), "a file outside the scripts dir was deleted")

	out, code = runUC(t, env, "which", "../victim")
	assert.Equal(t, 127, code, out)

	out, code = runUC(t, env, "../out")
	assert.Equal(t, 127, code, out)
	assert.NotContains(t, out, "OUTSIDE-RAN", "a script outside the scripts dir ran")
}

// improvement spec §4.2 and §4.3.
func TestBadNamesAndEmptyCommandsRejected(t *testing.T) {
	env := sandbox(t)
	for _, args := range [][]string{
		{"alias", "add", "a b", "ls"},
		{"function", "add", "a b", "ls"},
		{"alias", "add", "e", ""},
		{"alias", "add", "e", "   "},
		{"function", "add", "f", ""},
	} {
		out, code := runUC(t, env, args...)
		assert.Equalf(t, 1, code, "uc %q:\n%s", args, out)
	}
	out, _ := runUC(t, env, "list")
	assert.Contains(t, out, "Nothing registered yet")
}

// improvement spec §4.4: escape sequences in metadata never reach the
// terminal.
func TestMetadataEscapesStripped(t *testing.T) {
	env := sandbox(t)
	writeFile(t, "/tmp/esc.sh", "#!/bin/sh\n# @desc: \x1b[31mRED\x1b[0m\necho hi\n")
	out, code := runUC(t, env, "add", "/tmp/esc.sh", "esc")
	require.Zerof(t, code, "add:\n%s", out)
	out, code = runUC(t, env, "list")
	require.Zero(t, code)
	assert.NotContains(t, out, "\x1b", "escape sequence reached the terminal")
	assert.Contains(t, out, "[31mRED[0m")
}

// improvement spec §4.5.
func TestDotfilesNotListed(t *testing.T) {
	env := sandbox(t)
	writeFile(t, "/tmp/real.sh", "#!/bin/sh\n")
	_, code := runUC(t, env, "add", "/tmp/real.sh", "real")
	require.Zero(t, code)
	writeFile(t, sandboxPath(env, "UC_HOME")+"/scripts/.DS_Store", "junk")
	out, _ := runUC(t, env, "list")
	assert.NotContains(t, out, "DS_Store")
}

// improvement spec §4.6 and §4.9.
func TestHistoryOffAndClear(t *testing.T) {
	env := sandbox(t)
	_, code := runUC(t, env, "alias", "add", "hi", "echo", "hello")
	require.Zero(t, code)
	_, code = runUC(t, env, "hi", "--token=abc123")
	require.Zero(t, code)
	out, _ := runUC(t, env, "history")
	assert.Contains(t, out, "--token=abc123")

	out, code = runUC(t, env, "history", "clear")
	require.Zero(t, code)
	assert.Equal(t, "history cleared\n", out)
	out, _ = runUC(t, env, "history")
	assert.Equal(t, "no history yet\n", out)

	cfg := sandboxPath(env, "XDG_CONFIG_HOME") + "/uc"
	_, code = execIn(t, nil, "mkdir", "-p", cfg)
	require.Zero(t, code)
	writeFile(t, cfg+"/settings.json", `{"history_size": 0}`)
	_, code = execIn(t, nil, "rm", "-f", sandboxPath(env, "UC_HOME")+"/history.json")
	require.Zero(t, code)
	for range 3 {
		_, code = runUC(t, env, "hi")
		require.Zero(t, code)
	}
	assert.False(t, fileExists(t, sandboxPath(env, "UC_HOME")+"/history.json"), "history written with history_size 0")
	out, _ = runUC(t, env, "history")
	assert.Equal(t, "history is turned off (history_size is 0)\n", out)
}

// improvement spec §4.7: a URL can't smuggle a git option.
func TestSyncInitRejectsDashURL(t *testing.T) {
	env := sandbox(t)
	marker := sandboxRoot(t) + "/pwned"
	out, code := runUC(t, env, "sync", "init", "--", "--upload-pack=touch "+marker)
	assert.Equalf(t, 1, code, "output:\n%s", out)
	assert.Contains(t, out, "a remote URL cannot start with '-'")
	assert.False(t, fileExists(t, marker), "the injected command ran")
	assert.False(t, fileExists(t, sandboxPath(env, "UC_HOME")+"/sync"), "sync dir created")
}

// improvement spec §4.10.
func TestCreatedPathsArePrivate(t *testing.T) {
	env := sandbox(t)
	writeFile(t, "/tmp/p.sh", "#!/bin/sh\n")
	_, code := runUC(t, env, "add", "/tmp/p.sh", "p")
	require.Zero(t, code)
	_, code = runUC(t, env, "alias", "add", "gs", "git", "status")
	require.Zero(t, code)
	home, cfg := sandboxPath(env, "UC_HOME"), sandboxPath(env, "XDG_CONFIG_HOME")+"/uc"
	for path, want := range map[string]string{
		home: "700", home + "/scripts": "700", home + "/scripts/p": "700", cfg: "700", cfg + "/commands.json": "600",
	} {
		out, code := execIn(t, nil, "stat", "-c", "%a", path)
		require.Zerof(t, code, "stat %s", path)
		assert.Equal(t, want, strings.TrimSpace(out), path)
	}
}

// improvement spec §4.11: sync works on a machine with no git identity —
// the container has no global git config.
func TestSyncWithoutGitIdentity(t *testing.T) {
	root := sandboxRoot(t)
	env := sandboxAt(t, root+"/a")
	remote := root + "/remote.git"
	out, code := execIn(t, nil, "git", "init", "-q", "--bare", remote)
	require.Zerof(t, code, "git init --bare:\n%s", out)

	writeFile(t, root+"/s.sh", "#!/bin/sh\necho s\n")
	_, code = runUC(t, env, "add", root+"/s.sh", "s")
	require.Zero(t, code)
	out, code = runUC(t, env, "sync", "init", remote)
	require.Zerof(t, code, "sync init with no git identity:\n%s", out)
	assert.Contains(t, out, "pushed initial state")

	out, code = execIn(t, nil, "git", "-C", remote, "log", "-1", "--format=%an <%ae>")
	require.Zero(t, code)
	assert.True(t, strings.HasPrefix(out, "uc <uc@"), "commit author = %q", out)
}

// The strict alias rule (improvement spec §3.3).
func TestAliasDescStrictRule(t *testing.T) {
	env := sandbox(t)
	out, code := runUC(t, env, "alias", "add", "gs", "--desc", "short status", "echo", "status")
	require.Zerof(t, code, "output:\n%s", out)
	out, _ = runUC(t, env, "alias", "list")
	assert.Equal(t, "gs  short status\n", out)

	out, code = runUC(t, env, "alias", "add", "mk", "echo", "create", "--desc", "x", "--verbose")
	require.Zerof(t, code, "output:\n%s", out)
	assert.Contains(t, out, "added alias mk -> echo create --desc x --verbose")
	assert.Contains(t, out, "uc: note: '--desc' is part of the alias command")
	out, _ = runUC(t, env, "mk")
	assert.Equal(t, "create --desc x --verbose\n", out, "the aliased command keeps its own --desc")

	out, code = runUC(t, env, "alias", "add", "old", "echo", "hi", "--desc", "greet")
	require.Zero(t, code)
	assert.Contains(t, out, `uc alias add old --desc greet echo hi`, "the note shows the corrected command")

	out, code = runUC(t, env, "alias", "add", "dash", "--", "--weird")
	require.Zerof(t, code, "output:\n%s", out)
	assert.NotContains(t, out, "note:", "-- means the user was explicit")
}

// improvement spec §3.5: the picker explains itself without a terminal.
func TestPickerWithoutTerminal(t *testing.T) {
	env := sandbox(t)
	_, code := runUC(t, env, "alias", "add", "gs", "echo", "hi")
	require.Zero(t, code)
	out, code := runUC(t, env)
	assert.Equal(t, 1, code)
	assert.Equal(t, "uc: the interactive picker needs a terminal — run 'uc list' to see what is registered, or 'uc <name>' to run something\n", out)
}

// improvement spec §5.2: an install from the previous version is read in
// place and migrated on the first write.
func TestLegacyFilesMigrate(t *testing.T) {
	env := sandbox(t)
	cfg := sandboxPath(env, "XDG_CONFIG_HOME") + "/uc"
	_, code := execIn(t, nil, "mkdir", "-p", cfg)
	require.Zero(t, code)
	writeFile(t, cfg+"/aliases.json", `{"gs": {"command": "echo from-alias", "desc": "legacy alias", "usage": null, "examples": null}}`)
	writeFile(t, cfg+"/functions.json", `{"ship": {"body": "echo shipped $1", "desc": null, "usage": null, "examples": null}}`)

	out, code := runUC(t, env, "list")
	require.Zero(t, code)
	assert.Contains(t, out, "legacy alias")
	assert.Contains(t, out, "ship")
	assert.False(t, fileExists(t, cfg+"/commands.json"), "a read created commands.json")

	out, code = runUC(t, env, "ship", "it")
	require.Zero(t, code)
	assert.Equal(t, "shipped it\n", out)

	out, code = runUC(t, env, "alias", "add", "fresh", "echo", "fresh")
	require.Zerof(t, code, "output:\n%s", out)
	assert.Contains(t, out, "uc: note: merged aliases.json and functions.json into commands.json")
	assert.True(t, fileExists(t, cfg+"/commands.json"))
	assert.True(t, fileExists(t, cfg+"/aliases.json.migrated"))
	assert.False(t, fileExists(t, cfg+"/aliases.json"))

	out, _ = runUC(t, env, "gs")
	assert.Equal(t, "from-alias\n", out)
	out, code = execIn(t, nil, "cat", cfg+"/commands.json")
	require.Zero(t, code)
	assert.Contains(t, out, `"version": 1`)
	assert.NotContains(t, out, "null")
}

// improvement spec §5.3.
func TestExportImport(t *testing.T) {
	root := sandboxRoot(t)
	envA := sandboxAt(t, root+"/a")
	envB := sandboxAt(t, root+"/b")
	writeFile(t, root+"/killport.sh", "#!/bin/sh\n# @desc: kills a port\necho kill $1\n")
	_, code := runUC(t, envA, "add", root+"/killport.sh", "killport")
	require.Zero(t, code)
	_, code = runUC(t, envA, "alias", "add", "gs", "--desc", "short", "echo", "status")
	require.Zero(t, code)
	_, code = runUC(t, envA, "function", "add", "ship", "echo shipping $1")
	require.Zero(t, code)

	archive := root + "/uc.tar.gz"
	out, code := runUC(t, envA, "export", archive)
	require.Zerof(t, code, "export:\n%s", out)
	assert.Contains(t, out, "exported 1 script, 1 alias, 1 function to "+archive)
	assert.Contains(t, out, "treat it like a private key")
	mode, _ := execIn(t, nil, "stat", "-c", "%a", archive)
	assert.Equal(t, "600", strings.TrimSpace(mode))

	out, code = runUC(t, envA, "export", archive)
	assert.Equal(t, 1, code, out)
	assert.Contains(t, out, "already exists — pass --force to overwrite")

	out, code = runUC(t, envB, "import", archive, "--dry-run")
	require.Zerof(t, code, "dry run:\n%s", out)
	assert.Contains(t, out, "3 to add, 0 to replace — nothing written (--dry-run)")
	out, _ = runUC(t, envB, "list")
	assert.Contains(t, out, "Nothing registered yet", "--dry-run wrote something")

	out, code = runUC(t, envB, "import", archive)
	require.Zerof(t, code, "import:\n%s", out)
	assert.Contains(t, out, "imported 1 script, 1 alias, 1 function")
	out, code = runUC(t, envB, "killport", "80")
	require.Zero(t, code)
	assert.Equal(t, "kill 80\n", out)
	out, _ = runUC(t, envB, "ship", "now")
	assert.Equal(t, "shipping now\n", out)

	listA, _ := runUC(t, envA, "list")
	listB, _ := runUC(t, envB, "list")
	assert.Equal(t, listA, listB)

	// A conflicting local change blocks the whole import unless --force.
	_, code = runUC(t, envB, "alias", "remove", "gs")
	require.Zero(t, code)
	_, code = runUC(t, envB, "alias", "add", "gs", "echo", "mine")
	require.Zero(t, code)
	out, code = runUC(t, envB, "import", archive)
	assert.Equal(t, 1, code, out)
	assert.Contains(t, out, "1 entry already exists with different content:\n  ~ alias gs")
	out, _ = runUC(t, envB, "gs")
	assert.Equal(t, "mine\n", out, "a refused import changed an alias")
	_, code = runUC(t, envB, "import", archive, "--force")
	require.Zero(t, code)
	out, _ = runUC(t, envB, "gs")
	assert.Equal(t, "status\n", out)

	// Through a pipe: uc export - | uc import -
	envC := sandboxAt(t, root+"/c")
	script := `"$UC" export - | env UC_HOME="$C_HOME" XDG_CONFIG_HOME="$C_CFG" HOME="$C_HOME" "$UC" import -`
	out, code = execIn(t, withEnv(envA, "UC="+containerBinPath,
		"C_HOME="+sandboxPath(envC, "UC_HOME"), "C_CFG="+sandboxPath(envC, "XDG_CONFIG_HOME")), "sh", "-c", script)
	require.Zerof(t, code, "pipe:\n%s", out)
	listC, _ := runUC(t, envC, "list")
	assert.Equal(t, listA, listC)
}

// An archive from someone else is validated before anything is written.
func TestImportRejectsHostileArchive(t *testing.T) {
	env := sandbox(t)
	root := sandboxRoot(t)
	// A tar whose entry climbs out of the scripts directory.
	_, code := execIn(t, nil, "sh", "-c", `set -e
mkdir -p "$1/stage/scripts" && cd "$1/stage"
printf 'evil' > evil
tar -czf "$1/evil.tar.gz" --transform 's,^evil$,scripts/../../evil,' evil`, "sh", root)
	require.Zero(t, code, "build hostile archive")

	out, code := runUC(t, env, "import", root+"/evil.tar.gz")
	assert.Equal(t, 1, code, out)
	assert.Contains(t, out, "cannot import: unexpected entry 'scripts/../../evil'")
	assert.False(t, fileExists(t, sandboxPath(env, "UC_HOME")+"/../evil"))
	out, _ = runUC(t, env, "list")
	assert.Contains(t, out, "Nothing registered yet")
}
