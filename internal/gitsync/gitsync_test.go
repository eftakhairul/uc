package gitsync

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/eftakhairul/uc/internal/config"
	"github.com/eftakhairul/uc/internal/registry"
)

// setupGit skips the test when git is missing, and isolates git from the
// developer's own config (signing, hooks, default branch) with a fixed
// identity so commits work on a fresh CI machine.
func setupGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	emptyCfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(emptyCfg, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", emptyCfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "uc test")
	t.Setenv("GIT_AUTHOR_EMAIL", "uc@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "uc test")
	t.Setenv("GIT_COMMITTER_EMAIL", "uc@example.com")
}

// newRemote creates an empty bare repo standing in for the user's remote.
func newRemote(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	gitRun(t, "", "init", "-q", "--bare", dir)
	return dir
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// machine is one uc installation: its own UC_HOME and config dir.
type machine struct {
	cfg  *config.Config
	out  *bytes.Buffer
	warn *bytes.Buffer
	s    *Syncer
}

func newMachine(t *testing.T) *machine {
	t.Helper()
	root := t.TempDir()
	ucHome := filepath.Join(root, "uc-home")
	cfg := &config.Config{
		UCHome:     ucHome,
		ScriptsDir: filepath.Join(ucHome, "scripts"),
		ConfigDir:  filepath.Join(root, "config"),
	}
	m := &machine{cfg: cfg, out: &bytes.Buffer{}, warn: &bytes.Buffer{}}
	m.s = New(cfg, m.out)
	m.s.Warn = m.warn
	return m
}

func (m *machine) writeScript(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(m.cfg.ScriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(m.cfg.ScriptsDir, name), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (m *machine) writeAliases(t *testing.T, content string) {
	t.Helper()
	if err := os.MkdirAll(m.cfg.ConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(m.cfg.AliasesPath(), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (m *machine) script(t *testing.T, name string) (string, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(m.cfg.ScriptsDir, name))
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(data), true
}

func (m *machine) resetOut() {
	m.out.Reset()
	m.warn.Reset()
}

func commitCount(t *testing.T, remote string) int {
	t.Helper()
	out := gitRun(t, remote, "rev-list", "--all", "--count")
	n := 0
	for _, c := range out {
		n = n*10 + int(c-'0')
	}
	return n
}

func TestInitEmptyRemotePushesThenSecondMachineAdopts(t *testing.T) {
	setupGit(t)
	remote := newRemote(t)

	a := newMachine(t)
	a.writeScript(t, "killport", "#!/bin/sh\n# @desc: kills a port\necho kill $1\n")
	a.writeAliases(t, `{"gs":{"command":"git status"}}`)
	if err := a.s.Init(remote); err != nil {
		t.Fatalf("A init: %v", err)
	}
	if !strings.Contains(a.out.String(), "pushed initial state") {
		t.Errorf("A init output = %q, want 'pushed initial state'", a.out.String())
	}
	if !strings.Contains(a.out.String(), "+ scripts/killport") || !strings.Contains(a.out.String(), "+ aliases.json") {
		t.Errorf("A init output = %q, want per-file summary", a.out.String())
	}
	files := gitRun(t, remote, "ls-tree", "-r", "--name-only", "HEAD")
	for _, want := range []string{"scripts/killport", "aliases.json"} {
		if !strings.Contains(files, want) {
			t.Errorf("remote tree = %q, missing %s", files, want)
		}
	}

	b := newMachine(t)
	if err := b.s.Init(remote); err != nil {
		t.Fatalf("B init: %v", err)
	}
	if !strings.Contains(b.out.String(), "pulled existing state") {
		t.Errorf("B init output = %q, want 'pulled existing state'", b.out.String())
	}
	got, ok := b.script(t, "killport")
	if !ok || !strings.Contains(got, "echo kill") {
		t.Fatalf("B killport = %q, %v; want synced content", got, ok)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(b.cfg.ScriptsDir, "killport"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("B killport mode = %v, want exec bit", info.Mode())
		}
	}
	aliases, err := os.ReadFile(b.cfg.AliasesPath())
	if err != nil || !strings.Contains(string(aliases), "git status") {
		t.Errorf("B aliases.json = %q, %v; want synced", aliases, err)
	}

	// The synced script is a real registered script on B.
	reg := registry.New(b.cfg.ScriptsDir, b.cfg.AliasesPath(), b.cfg.FunctionsPath())
	res, err := reg.Resolve("killport")
	if err != nil || res.Desc != "kills a port" {
		t.Errorf("B Resolve(killport) = %+v, %v", res, err)
	}
}

func TestInitTwiceErrors(t *testing.T) {
	setupGit(t)
	remote := newRemote(t)
	a := newMachine(t)
	if err := a.s.Init(remote); err != nil {
		t.Fatalf("first init: %v", err)
	}
	if !strings.Contains(a.out.String(), "nothing to push yet") {
		t.Errorf("init with empty state output = %q, want 'nothing to push yet'", a.out.String())
	}
	err := a.s.Init(remote)
	if err == nil || !strings.Contains(err.Error(), "already initialized") {
		t.Fatalf("second init error = %v, want 'already initialized'", err)
	}
}

func TestInitRefusesToOverwriteDifferingLocalState(t *testing.T) {
	setupGit(t)
	remote := newRemote(t)
	a := newMachine(t)
	a.writeScript(t, "from-a", "#!/bin/sh\necho a\n")
	if err := a.s.Init(remote); err != nil {
		t.Fatalf("A init: %v", err)
	}

	b := newMachine(t)
	b.writeScript(t, "from-b", "#!/bin/sh\necho b\n")
	err := b.s.Init(remote)
	if err == nil || !strings.Contains(err.Error(), "differ") || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("B init error = %v, want differing-state refusal mentioning --force", err)
	}
	if _, ok := b.script(t, "from-b"); !ok {
		t.Error("B's own script was deleted by a refused init")
	}

	// --force adopts the remote's state, discarding B's.
	if err := b.s.Pull(true); err != nil {
		t.Fatalf("B pull --force: %v", err)
	}
	if _, ok := b.script(t, "from-b"); ok {
		t.Error("from-b survived pull --force")
	}
	if _, ok := b.script(t, "from-a"); !ok {
		t.Error("from-a missing after pull --force")
	}
}

func TestPushPropagatesAddsUpdatesDeletes(t *testing.T) {
	setupGit(t)
	remote := newRemote(t)
	a, b := newMachine(t), newMachine(t)
	a.writeScript(t, "keep", "#!/bin/sh\necho v1\n")
	a.writeScript(t, "old", "#!/bin/sh\necho old\n")
	if err := a.s.Init(remote); err != nil {
		t.Fatalf("A init: %v", err)
	}
	if err := b.s.Init(remote); err != nil {
		t.Fatalf("B init: %v", err)
	}

	a.resetOut()
	a.writeScript(t, "keep", "#!/bin/sh\necho v2\n")
	a.writeScript(t, "new", "#!/bin/sh\necho new\n")
	if err := os.Remove(filepath.Join(a.cfg.ScriptsDir, "old")); err != nil {
		t.Fatal(err)
	}
	if err := a.s.Push(); err != nil {
		t.Fatalf("A push: %v", err)
	}
	for _, want := range []string{"~ scripts/keep", "+ scripts/new", "- scripts/old", "pushed"} {
		if !strings.Contains(a.out.String(), want) {
			t.Errorf("A push output = %q, missing %q", a.out.String(), want)
		}
	}

	b.resetOut()
	if err := b.s.Pull(false); err != nil {
		t.Fatalf("B pull: %v", err)
	}
	if got, _ := b.script(t, "keep"); !strings.Contains(got, "v2") {
		t.Errorf("B keep = %q, want v2", got)
	}
	if _, ok := b.script(t, "new"); !ok {
		t.Error("B missing new script")
	}
	if _, ok := b.script(t, "old"); ok {
		t.Error("B still has deleted script")
	}

	b.resetOut()
	if err := b.s.Pull(false); err != nil {
		t.Fatalf("B second pull: %v", err)
	}
	if !strings.Contains(b.out.String(), "already up to date") {
		t.Errorf("B second pull output = %q, want 'already up to date'", b.out.String())
	}
}

func TestPushNothingChangedMakesNoCommit(t *testing.T) {
	setupGit(t)
	remote := newRemote(t)
	a := newMachine(t)
	a.writeScript(t, "x", "#!/bin/sh\n")
	if err := a.s.Init(remote); err != nil {
		t.Fatalf("init: %v", err)
	}
	before := commitCount(t, remote)

	a.resetOut()
	if err := a.s.Push(); err != nil {
		t.Fatalf("push: %v", err)
	}
	if !strings.Contains(a.out.String(), "nothing to sync") {
		t.Errorf("push output = %q, want 'nothing to sync'", a.out.String())
	}
	if after := commitCount(t, remote); after != before {
		t.Errorf("remote commits %d -> %d, want unchanged", before, after)
	}
}

func TestPushRejectedThenPullMergesNonConflictingChanges(t *testing.T) {
	setupGit(t)
	remote := newRemote(t)
	a, b := newMachine(t), newMachine(t)
	a.writeScript(t, "shared", "#!/bin/sh\n")
	if err := a.s.Init(remote); err != nil {
		t.Fatalf("A init: %v", err)
	}
	if err := b.s.Init(remote); err != nil {
		t.Fatalf("B init: %v", err)
	}

	a.writeScript(t, "from-a", "#!/bin/sh\necho a\n")
	if err := a.s.Push(); err != nil {
		t.Fatalf("A push: %v", err)
	}

	b.writeScript(t, "from-b", "#!/bin/sh\necho b\n")
	err := b.s.Push()
	if err == nil || !strings.Contains(err.Error(), "run uc sync pull first") {
		t.Fatalf("B push error = %v, want pull-first rejection", err)
	}

	// B's change is committed in its staging repo, so pull may proceed
	// and replays it on top of A's.
	b.resetOut()
	if err := b.s.Pull(false); err != nil {
		t.Fatalf("B pull: %v", err)
	}
	if !strings.Contains(b.out.String(), "not yet pushed") {
		t.Errorf("B pull output = %q, want unpushed-commits hint", b.out.String())
	}
	for _, name := range []string{"from-a", "from-b", "shared"} {
		if _, ok := b.script(t, name); !ok {
			t.Errorf("B missing %s after pull", name)
		}
	}
	if err := b.s.Push(); err != nil {
		t.Fatalf("B push after pull: %v", err)
	}
	if err := a.s.Pull(false); err != nil {
		t.Fatalf("A pull: %v", err)
	}
	if _, ok := a.script(t, "from-b"); !ok {
		t.Error("A missing from-b after round-trip")
	}
}

func TestPullConflictReportsDiverged(t *testing.T) {
	setupGit(t)
	remote := newRemote(t)
	a, b := newMachine(t), newMachine(t)
	a.writeScript(t, "shared", "#!/bin/sh\necho base\n")
	if err := a.s.Init(remote); err != nil {
		t.Fatalf("A init: %v", err)
	}
	if err := b.s.Init(remote); err != nil {
		t.Fatalf("B init: %v", err)
	}

	a.writeScript(t, "shared", "#!/bin/sh\necho from a\n")
	if err := a.s.Push(); err != nil {
		t.Fatalf("A push: %v", err)
	}
	b.writeScript(t, "shared", "#!/bin/sh\necho from b\n")
	if err := b.s.Push(); err == nil {
		t.Fatal("B push succeeded, want rejection")
	}

	err := b.s.Pull(false)
	if err == nil || !strings.Contains(err.Error(), "diverged") {
		t.Fatalf("B pull error = %v, want 'diverged'", err)
	}
	// The failed rebase was aborted and B's live file is untouched.
	if got, _ := b.script(t, "shared"); !strings.Contains(got, "from b") {
		t.Errorf("B shared = %q, want B's own edit kept", got)
	}
	if _, err := os.Stat(filepath.Join(b.s.Dir, ".git", "rebase-merge")); !os.IsNotExist(err) {
		t.Error("rebase left in progress in the staging repo")
	}
}

func TestPullRefusesUnpushedLocalChanges(t *testing.T) {
	setupGit(t)
	remote := newRemote(t)
	a := newMachine(t)
	a.writeScript(t, "x", "#!/bin/sh\n")
	if err := a.s.Init(remote); err != nil {
		t.Fatalf("init: %v", err)
	}

	a.writeScript(t, "unpushed", "#!/bin/sh\necho mine\n")
	err := a.s.Pull(false)
	if err == nil || !strings.Contains(err.Error(), "+ scripts/unpushed") {
		t.Fatalf("pull error = %v, want refusal listing the unpushed script", err)
	}
	if _, ok := a.script(t, "unpushed"); !ok {
		t.Fatal("refused pull deleted the unpushed script")
	}

	if err := a.s.Pull(true); err != nil {
		t.Fatalf("pull --force: %v", err)
	}
	if _, ok := a.script(t, "unpushed"); ok {
		t.Error("pull --force kept the unpushed script")
	}
}

func TestPullAfterEmptyInitTracksBranchPushedElsewhere(t *testing.T) {
	setupGit(t)
	remote := newRemote(t)
	a, b := newMachine(t), newMachine(t)

	// Both machines start empty: neither has anything to publish yet.
	if err := a.s.Init(remote); err != nil {
		t.Fatalf("A init: %v", err)
	}
	if err := b.s.Init(remote); err != nil {
		t.Fatalf("B init: %v", err)
	}
	b.resetOut()
	if err := b.s.Pull(false); err != nil {
		t.Fatalf("B pull of empty remote: %v", err)
	}
	if !strings.Contains(b.out.String(), "remote is empty") {
		t.Errorf("B pull output = %q, want 'remote is empty'", b.out.String())
	}

	a.writeScript(t, "first", "#!/bin/sh\n")
	if err := a.s.Push(); err != nil {
		t.Fatalf("A push: %v", err)
	}
	if err := b.s.Pull(false); err != nil {
		t.Fatalf("B pull: %v", err)
	}
	if _, ok := b.script(t, "first"); !ok {
		t.Error("B missing script pushed by A")
	}
	b.writeScript(t, "second", "#!/bin/sh\n")
	if err := b.s.Push(); err != nil {
		t.Fatalf("B push: %v", err)
	}
}

func TestInitHandlesRemoteHeadNamingUnpushedBranch(t *testing.T) {
	setupGit(t)
	remote := newRemote(t)
	a, b := newMachine(t), newMachine(t)
	a.writeScript(t, "x", "#!/bin/sh\necho x\n")
	if err := a.s.Init(remote); err != nil {
		t.Fatalf("A init: %v", err)
	}
	// Point the remote's HEAD at a branch nobody pushed, as when a hosted
	// repo's default branch name differs from the one the first push used.
	// A clone then checks nothing out.
	gitRun(t, remote, "symbolic-ref", "HEAD", "refs/heads/does-not-exist")
	if err := b.s.Init(remote); err != nil {
		t.Fatalf("B init: %v", err)
	}
	if !strings.Contains(b.out.String(), "pulled existing state") {
		t.Errorf("B init output = %q, want adoption, not a fresh push", b.out.String())
	}
	if _, ok := b.script(t, "x"); !ok {
		t.Error("B missing script")
	}
}

func TestStatus(t *testing.T) {
	setupGit(t)
	remote := newRemote(t)
	a, b := newMachine(t), newMachine(t)
	a.writeScript(t, "x", "#!/bin/sh\n")
	if err := a.s.Init(remote); err != nil {
		t.Fatalf("A init: %v", err)
	}
	if err := b.s.Init(remote); err != nil {
		t.Fatalf("B init: %v", err)
	}

	a.resetOut()
	if err := a.s.Status(); err != nil {
		t.Fatalf("status: %v", err)
	}
	for _, want := range []string{"remote: " + remote, "no local changes", "up to date"} {
		if !strings.Contains(a.out.String(), want) {
			t.Errorf("status output = %q, missing %q", a.out.String(), want)
		}
	}

	a.writeScript(t, "y", "#!/bin/sh\n")
	a.resetOut()
	if err := a.s.Status(); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(a.out.String(), "+ scripts/y") {
		t.Errorf("status output = %q, want unpushed + scripts/y", a.out.String())
	}

	if err := a.s.Push(); err != nil {
		t.Fatalf("push: %v", err)
	}
	b.resetOut()
	if err := b.s.Status(); err != nil {
		t.Fatalf("B status: %v", err)
	}
	if !strings.Contains(b.out.String(), "0 commit(s) to push, 1 to pull") {
		t.Errorf("B status output = %q, want behind by 1", b.out.String())
	}
}

func TestNotInitialized(t *testing.T) {
	setupGit(t)
	m := newMachine(t)
	for name, fn := range map[string]func() error{
		"push":   m.s.Push,
		"pull":   func() error { return m.s.Pull(false) },
		"status": m.s.Status,
	} {
		err := fn()
		if err == nil || !strings.Contains(err.Error(), "uc sync init") {
			t.Errorf("%s error = %v, want 'run uc sync init' hint", name, err)
		}
	}
}

func TestMissingGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	m := newMachine(t)
	if err := m.s.Init("/nowhere"); err == nil || !strings.Contains(err.Error(), "git not found") {
		t.Errorf("Init error = %v, want 'git not found'", err)
	}
	if err := m.s.Push(); err == nil || !strings.Contains(err.Error(), "git not found") {
		t.Errorf("Push error = %v, want 'git not found'", err)
	}
}

func TestSyncDirIsNotAScript(t *testing.T) {
	setupGit(t)
	remote := newRemote(t)
	a := newMachine(t)
	a.writeScript(t, "x", "#!/bin/sh\n")
	if err := a.s.Init(remote); err != nil {
		t.Fatalf("init: %v", err)
	}
	reg := registry.New(a.cfg.ScriptsDir, a.cfg.AliasesPath(), a.cfg.FunctionsPath())
	items, err := reg.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Name == "sync" || strings.Contains(it.Name, ".git") {
			t.Errorf("List() includes staging repo entry %q", it.Name)
		}
	}
}

func TestMirror(t *testing.T) {
	root := t.TempDir()
	mk := func(name string) tree {
		d := filepath.Join(root, name)
		return tree{
			scripts:   filepath.Join(d, "scripts"),
			aliases:   filepath.Join(d, "aliases.json"),
			functions: filepath.Join(d, "functions.json"),
		}
	}
	src, dst := mk("src"), mk("dst")
	write := func(path, content string, mode os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(src.scripts, "added"), "a", 0o644)
	write(filepath.Join(src.scripts, "changed"), "new", 0o644)
	write(filepath.Join(src.scripts, "same"), "s", 0o644)
	write(filepath.Join(src.scripts, ".DS_Store"), "junk", 0o644)
	if err := os.MkdirAll(filepath.Join(src.scripts, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(src.aliases, `{"a":1}`, 0o600)
	write(filepath.Join(dst.scripts, "changed"), "old", 0o755)
	write(filepath.Join(dst.scripts, "same"), "s", 0o755)
	write(filepath.Join(dst.scripts, "gone"), "g", 0o755)
	write(dst.functions, `{}`, 0o600)

	var warn bytes.Buffer
	changes, err := mirror(src, dst, &warn)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range changes {
		got = append(got, c.String())
	}
	want := []string{"+ scripts/added", "~ scripts/changed", "- scripts/gone", "+ aliases.json", "- functions.json"}
	if !slices.Equal(got, want) {
		t.Errorf("changes = %q, want %q", got, want)
	}
	if !strings.Contains(warn.String(), "subdir") {
		t.Errorf("warnings = %q, want subdir skipped", warn.String())
	}

	if data, _ := os.ReadFile(filepath.Join(dst.scripts, "changed")); string(data) != "new" {
		t.Errorf("dst changed = %q, want new", data)
	}
	if _, err := os.Stat(filepath.Join(dst.scripts, "gone")); !os.IsNotExist(err) {
		t.Error("dst gone still exists")
	}
	if _, err := os.Stat(filepath.Join(dst.scripts, ".DS_Store")); !os.IsNotExist(err) {
		t.Error("dotfile was synced")
	}
	if _, err := os.Stat(filepath.Join(dst.scripts, "subdir")); !os.IsNotExist(err) {
		t.Error("subdirectory was synced")
	}
	if _, err := os.Stat(dst.functions); !os.IsNotExist(err) {
		t.Error("dst functions.json not deleted")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dst.scripts, "added"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Errorf("dst added mode = %v, want 0755", info.Mode().Perm())
		}
	}

	// A second mirror is a no-op.
	changes, err = mirror(src, dst, &warn)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Errorf("second mirror changes = %v, want none", changes)
	}
}
