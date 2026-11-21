// Package gitsync implements `uc sync`: syncing scripts, aliases, and
// functions across machines through a git remote the user owns. uc shells
// out to git; there is no hosted backend.
//
// The remote is cloned into a staging repo ($UC_HOME/sync) laid out as
//
//	scripts/        mirror of $UC_HOME/scripts
//	commands.json   copy of $XDG_CONFIG_HOME/uc/commands.json
//
// A repo still in the pre-merge layout (aliases.json + functions.json) is
// converted to commands.json in a commit of its own the first time this
// version touches it.
//
// rather than making the live directories a repo, so one repo can span the
// two live trees and .git stays out of them. history.json and
// settings.json are per-machine and never synced.
//
// Push treats the live files as truth and pull treats the repo as truth
// (mirror semantics), so deletions propagate in both directions.
package gitsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/eftakhairul/uc/internal/atomicfile"
	"github.com/eftakhairul/uc/internal/config"
	"github.com/eftakhairul/uc/internal/store"
)

const (
	scriptsName  = "scripts"
	commandsName = "commands.json"

	// The pre-merge layout, converted on first contact.
	legacyAliasesName   = "aliases.json"
	legacyFunctionsName = "functions.json"
)

// Git timeouts: talking to the remote can hang on an unreachable host, so
// quick network calls get a short deadline and transfers a longer one.
var (
	quickTimeout    = 30 * time.Second
	transferTimeout = 5 * time.Minute
)

// Syncer runs sync operations between the live uc files and the staging
// repo at Dir.
type Syncer struct {
	Dir          string // staging repo, $UC_HOME/sync
	ScriptsDir   string // live scripts
	CommandsPath string // live commands.json
	Store        *store.Store
	Out          io.Writer
	Warn         io.Writer // non-fatal warnings (skipped files, offline fetch)
}

func New(cfg *config.Config, out io.Writer) *Syncer {
	return &Syncer{
		Dir:          cfg.SyncDir(),
		ScriptsDir:   cfg.ScriptsDir,
		CommandsPath: cfg.CommandsPath(),
		Store:        cfg.Store(),
		Out:          out,
		Warn:         os.Stderr,
	}
}

// tree is one side of a mirror: a scripts directory plus commands.json.
type tree struct {
	scripts  string
	commands string
}

func (s *Syncer) live() tree {
	return tree{scripts: s.ScriptsDir, commands: s.CommandsPath}
}

func (s *Syncer) repo() tree {
	return tree{
		scripts:  filepath.Join(s.Dir, scriptsName),
		commands: filepath.Join(s.Dir, commandsName),
	}
}

// migrateLive makes sure the live side is in the commands.json layout
// before it is compared with the repo.
func (s *Syncer) migrateLive() error {
	if s.Store == nil {
		return nil
	}
	return s.Store.Migrate()
}

// change is one file-level difference between two trees, keyed by its
// repo-relative path ("scripts/killport", "commands.json").
type change struct {
	op   byte // '+' added, '~' content changed, '-' deleted
	path string
}

func (c change) String() string { return string(c.op) + " " + c.path }

// diff reports what mirror(src, dst) would change, without writing.
// skipped lists non-regular entries in src's scripts dir, which are never
// synced.
func diff(src, dst tree) (changes []change, skipped []string, err error) {
	srcNames, skipped, err := listScripts(src.scripts)
	if err != nil {
		return nil, nil, err
	}
	dstNames, _, err := listScripts(dst.scripts)
	if err != nil {
		return nil, nil, err
	}

	for _, name := range srcNames {
		rel := scriptsName + "/" + name
		if !slices.Contains(dstNames, name) {
			changes = append(changes, change{'+', rel})
			continue
		}
		same, err := sameContent(filepath.Join(src.scripts, name), filepath.Join(dst.scripts, name))
		if err != nil {
			return nil, nil, err
		}
		if !same {
			changes = append(changes, change{'~', rel})
		}
	}
	for _, name := range dstNames {
		if !slices.Contains(srcNames, name) {
			changes = append(changes, change{'-', scriptsName + "/" + name})
		}
	}

	for _, f := range []struct{ rel, src, dst string }{
		{commandsName, src.commands, dst.commands},
	} {
		srcOK, err := exists(f.src)
		if err != nil {
			return nil, nil, err
		}
		dstOK, err := exists(f.dst)
		if err != nil {
			return nil, nil, err
		}
		switch {
		case srcOK && !dstOK:
			changes = append(changes, change{'+', f.rel})
		case !srcOK && dstOK:
			changes = append(changes, change{'-', f.rel})
		case srcOK && dstOK:
			same, err := sameContent(f.src, f.dst)
			if err != nil {
				return nil, nil, err
			}
			if !same {
				changes = append(changes, change{'~', f.rel})
			}
		}
	}
	return changes, skipped, nil
}

// mirror makes dst's managed set identical to src's: scripts/ plus
// commands.json. Scripts land with config.ScriptMode. Returns the changes
// it made.
func mirror(src, dst tree, warn io.Writer) ([]change, error) {
	changes, skipped, err := diff(src, dst)
	if err != nil {
		return nil, err
	}
	for _, name := range skipped {
		fmt.Fprintf(warn, "uc: warning: skipping %s — only regular files are synced\n", filepath.Join(src.scripts, name))
	}

	for _, c := range changes {
		var from, to string
		switch c.path {
		case commandsName:
			from, to = src.commands, dst.commands
		default:
			name := strings.TrimPrefix(c.path, scriptsName+"/")
			from, to = filepath.Join(src.scripts, name), filepath.Join(dst.scripts, name)
		}

		if c.op == '-' {
			if err := os.Remove(to); err != nil {
				return nil, fmt.Errorf("remove %s: %w", to, err)
			}
			continue
		}
		data, err := os.ReadFile(from)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", from, err)
		}
		if err := atomicfile.Write(to, data); err != nil {
			return nil, err
		}
		// atomicfile.Write leaves the temp file's 0o600 mode; scripts must
		// stay executable on the receiving side.
		if strings.HasPrefix(c.path, scriptsName+"/") {
			if err := os.Chmod(to, config.ScriptMode); err != nil {
				return nil, fmt.Errorf("chmod %s: %w", to, err)
			}
		}
	}
	return changes, nil
}

// listScripts returns the sorted names of the regular files in dir (a
// missing dir is empty), plus the names of non-regular entries it skipped.
// Dotfiles (.DS_Store and friends) are ignored entirely.
func listScripts(dir string) (names, skipped []string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read %s: %w", dir, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !e.Type().IsRegular() {
			skipped = append(skipped, e.Name())
			continue
		}
		names = append(names, e.Name())
	}
	return names, skipped, nil // os.ReadDir already sorts by name
}

func sameContent(a, b string) (bool, error) {
	da, err := os.ReadFile(a)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", a, err)
	}
	db, err := os.ReadFile(b)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", b, err)
	}
	return bytes.Equal(da, db), nil
}

func exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, fmt.Errorf("stat %s: %w", path, err)
}

// hasState reports whether t holds anything worth syncing.
func hasState(t tree) (bool, error) {
	names, _, err := listScripts(t.scripts)
	if err != nil || len(names) > 0 {
		return len(names) > 0, err
	}
	return exists(t.commands)
}

func (s *Syncer) printChanges(changes []change) {
	for _, c := range changes {
		fmt.Fprintln(s.Out, c)
	}
}

var errNoGit = errors.New("git not found on PATH — uc sync requires git")

func requireGit() error {
	if _, err := exec.LookPath("git"); err != nil {
		return errNoGit
	}
	return nil
}

// prepare runs before push, pull, and status: git and an initialized repo
// are required, and both sides are brought to the commands.json layout so
// they compare cleanly.
func (s *Syncer) prepare() error {
	if err := s.requireInit(); err != nil {
		return err
	}
	if err := s.migrateLive(); err != nil {
		return err
	}
	if s.hasHead() {
		return s.convertLegacyRepo()
	}
	return nil
}

// requireInit checks for git and an initialized staging repo.
func (s *Syncer) requireInit() error {
	if err := requireGit(); err != nil {
		return err
	}
	ok, err := exists(filepath.Join(s.Dir, ".git"))
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("sync is not set up — run uc sync init <remote-url> first")
	}
	return nil
}

// gitError is a git failure explained in terms of what uc was doing, with
// git's own output (usually the most useful part) indented below. The git
// command line itself is internal and not shown.
type gitError struct {
	doing  string
	output string
	err    error
}

func (e *gitError) Error() string {
	if errors.Is(e.err, context.DeadlineExceeded) {
		return fmt.Sprintf("sync failed while %s: could not reach the remote (timed out)", e.doing)
	}
	if e.output == "" {
		return fmt.Sprintf("sync failed while %s: git exited with an error", e.doing)
	}
	return fmt.Sprintf("sync failed while %s:\n  %s", e.doing, strings.ReplaceAll(e.output, "\n", "\n  "))
}

func (e *gitError) Unwrap() error { return e.err }

// git runs a git subcommand inside the staging repo, for internal checks
// whose failure is an answer rather than a problem (rev-parse and the like).
func (s *Syncer) git(args ...string) (string, error) {
	return runGit(transferTimeout, append([]string{"-C", s.Dir}, args...)...)
}

// gitDo is git for steps whose failure the user should see: the error says
// what uc was doing.
func (s *Syncer) gitDo(doing string, args ...string) (string, error) {
	return s.gitDoTimeout(transferTimeout, doing, args...)
}

func (s *Syncer) gitDoTimeout(timeout time.Duration, doing string, args ...string) (string, error) {
	out, err := runGit(timeout, append([]string{"-C", s.Dir}, args...)...)
	if err != nil {
		return out, &gitError{doing: doing, output: out, err: err}
	}
	return out, nil
}

// runGit runs git with a deadline, returning its trimmed combined output.
func runGit(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	// Never stop to ask for credentials: a prompt inside uc would hang
	// with no explanation. Credential helpers and SSH agents still work.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if ctx.Err() != nil {
		return text, ctx.Err()
	}
	return text, err
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}

// commitAll stages everything in the repo and commits it, reporting
// whether there was anything to commit.
func (s *Syncer) commitAll(msg string) (bool, error) {
	if _, err := s.gitDo("committing", "add", "-A"); err != nil {
		return false, err
	}
	status, err := s.gitDo("committing", "status", "--porcelain")
	if err != nil {
		return false, err
	}
	if status == "" {
		return false, nil
	}
	if err := s.commit(msg); err != nil {
		return false, err
	}
	return true, nil
}

// commit commits what is staged, independent of the user's git config: a
// machine with no git identity gets a fallback one (sync is exactly what a
// fresh machine runs first), and commit signing is turned off so a GPG
// setup can't prompt or fail inside a non-interactive call.
func (s *Syncer) commit(msg string) error {
	args := []string{"-c", "commit.gpgsign=false"}
	if !s.hasIdentity() {
		args = append(args, "-c", "user.name=uc", "-c", "user.email=uc@"+hostname())
	}
	args = append(args, "commit", "-q", "-m", msg)
	_, err := s.gitDo("committing", args...)
	return err
}

// hasIdentity reports whether git has a committer identity for this repo,
// from config or the environment.
func (s *Syncer) hasIdentity() bool {
	if os.Getenv("GIT_COMMITTER_EMAIL") != "" || os.Getenv("EMAIL") != "" {
		return true
	}
	_, err := s.git("config", "user.email")
	return err == nil
}

// convertLegacyRepo moves a repo still in the pre-merge layout
// (aliases.json + functions.json) to commands.json, in a commit of its own,
// so machines on this version share one layout. It is a no-op when the repo
// already has commands.json or has neither legacy file. The commit is
// published by the next push.
func (s *Syncer) convertLegacyRepo() error {
	repoStore := store.New(filepath.Join(s.Dir, commandsName),
		filepath.Join(s.Dir, legacyAliasesName), filepath.Join(s.Dir, legacyFunctionsName))
	repoStore.Warn = s.Warn
	if ok, err := exists(repoStore.Path); err != nil || ok {
		return err
	}
	aOK, err := exists(repoStore.LegacyAliases)
	if err != nil {
		return err
	}
	fOK, err := exists(repoStore.LegacyFunctions)
	if err != nil {
		return err
	}
	if !aOK && !fOK {
		return nil
	}

	f, err := repoStore.Load()
	if err != nil {
		return err
	}
	data, err := store.EncodeAll(f)
	if err != nil {
		return err
	}
	if err := atomicfile.Write(repoStore.Path, data); err != nil {
		return err
	}
	if _, err := s.gitDo("converting the sync repo", "rm", "-q", "--ignore-unmatch", "--cached", legacyAliasesName, legacyFunctionsName); err != nil {
		return err
	}
	for _, p := range []string{repoStore.LegacyAliases, repoStore.LegacyFunctions} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", p, err)
		}
	}
	if _, err := s.gitDo("converting the sync repo", "add", commandsName); err != nil {
		return err
	}
	if err := s.commit("migrate to commands.json"); err != nil {
		return err
	}
	fmt.Fprintln(s.Warn, "uc: note: converted the sync repo from aliases.json/functions.json to commands.json — "+
		"upgrade uc on your other machines before they sync again")
	return nil
}

// hasUpstream reports whether the current branch tracks a remote branch.
func (s *Syncer) hasUpstream() bool {
	_, err := s.git("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	return err == nil
}

// hasHead reports whether the repo has at least one commit.
func (s *Syncer) hasHead() bool {
	_, err := s.git("rev-parse", "--verify", "-q", "HEAD")
	return err == nil
}

// aheadBehind counts commits on HEAD not on the upstream (ahead) and on the
// upstream not on HEAD (behind). Requires an upstream.
func (s *Syncer) aheadBehind() (ahead, behind int, err error) {
	out, err := s.gitDo("comparing with the remote", "rev-list", "--left-right", "--count", "@{u}...HEAD")
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("unexpected git rev-list output %q", out)
	}
	if behind, err = strconv.Atoi(fields[0]); err != nil {
		return 0, 0, fmt.Errorf("unexpected git rev-list output %q", out)
	}
	if ahead, err = strconv.Atoi(fields[1]); err != nil {
		return 0, 0, fmt.Errorf("unexpected git rev-list output %q", out)
	}
	return ahead, behind, nil
}

// push publishes HEAD, setting the upstream on first push.
func (s *Syncer) push() error {
	out, err := s.gitDo("pushing", "push", "-q", "-u", "origin", "HEAD")
	if err != nil {
		if strings.Contains(out, "[rejected]") || strings.Contains(out, "non-fast-forward") || strings.Contains(out, "fetch first") {
			return errors.New("remote has newer changes — run uc sync pull first")
		}
		return err
	}
	return nil
}

// Init clones remoteURL into the staging repo. A remote that already holds
// state is adopted into this machine; an empty one receives this
// machine's state.
func (s *Syncer) Init(remoteURL string) error {
	// git reads an argument starting with '-' as an option, and
	// --upload-pack=<command> would run that command.
	if strings.HasPrefix(remoteURL, "-") {
		return fmt.Errorf("invalid remote '%s' — a remote URL cannot start with '-'", remoteURL)
	}
	if err := requireGit(); err != nil {
		return err
	}
	ok, err := exists(filepath.Join(s.Dir, ".git"))
	if err != nil {
		return err
	}
	if ok {
		return fmt.Errorf("already initialized — %s", s.Dir)
	}
	if err := s.migrateLive(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Dir), config.DirMode); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(s.Dir), err)
	}
	// autocrlf off: scripts must round-trip byte-for-byte on every OS.
	// "--" keeps the URL from ever being read as an option.
	if out, err := runGit(transferTimeout, "clone", "-q", "-c", "core.autocrlf=false", "--", remoteURL, s.Dir); err != nil {
		return &gitError{doing: "cloning " + remoteURL, output: out, err: err}
	}
	// A remote whose HEAD names a branch nobody pushed (e.g. HEAD -> main
	// but the first machine pushed master) clones with nothing checked
	// out; fall back to whatever branch it does have.
	if !s.hasHead() {
		if _, err := s.trackRemoteBranch(); err != nil {
			return err
		}
	}
	if s.hasHead() {
		if err := s.convertLegacyRepo(); err != nil {
			return err
		}
	}

	remoteHas, err := hasState(s.repo())
	if err != nil {
		return err
	}
	if remoteHas {
		if err := s.adoptOnInit(remoteURL); err != nil {
			return err
		}
		// A conversion commit made above still needs publishing.
		if ahead, _, err := s.aheadBehind(); err == nil && ahead > 0 {
			return s.push()
		}
		return nil
	}

	changes, err := mirror(s.live(), s.repo(), s.Warn)
	if err != nil {
		return err
	}
	committed, err := s.commitAll("initial sync from " + hostname())
	if err != nil {
		return err
	}
	if !committed {
		fmt.Fprintf(s.Out, "initialized sync with %s — nothing to push yet\n", remoteURL)
		return nil
	}
	if err := s.push(); err != nil {
		return err
	}
	s.printChanges(changes)
	fmt.Fprintf(s.Out, "pushed initial state to %s\n", remoteURL)
	return nil
}

// adoptOnInit pulls a non-empty remote's state into this machine — unless
// this machine already has differing state of its own, which init must
// never silently discard.
func (s *Syncer) adoptOnInit(remoteURL string) error {
	localHas, err := hasState(s.live())
	if err != nil {
		return err
	}
	if localHas {
		local, _, err := diff(s.live(), s.repo())
		if err != nil {
			return err
		}
		if len(local) > 0 {
			return fmt.Errorf("this machine's scripts/aliases/functions differ from %s — "+
				"run uc sync pull --force to replace them with the remote's, "+
				"or uc sync push to replace the remote's with them", remoteURL)
		}
	}
	changes, err := mirror(s.repo(), s.live(), s.Warn)
	if err != nil {
		return err
	}
	s.printChanges(changes)
	fmt.Fprintf(s.Out, "pulled existing state from %s\n", remoteURL)
	return nil
}

// Push mirrors the live files into the staging repo, commits, and pushes.
func (s *Syncer) Push() error {
	if err := s.prepare(); err != nil {
		return err
	}
	changes, err := mirror(s.live(), s.repo(), s.Warn)
	if err != nil {
		return err
	}
	if _, err := s.commitAll(fmt.Sprintf("sync from %s at %s", hostname(), time.Now().Format(time.RFC3339))); err != nil {
		return err
	}

	// A commit left behind by an earlier rejected push still needs
	// publishing even when nothing changed since.
	needPush := false
	if s.hasHead() {
		if !s.hasUpstream() {
			needPush = true
		} else {
			ahead, _, err := s.aheadBehind()
			if err != nil {
				return err
			}
			needPush = ahead > 0
		}
	}
	if !needPush {
		fmt.Fprintln(s.Out, "nothing to sync")
		return nil
	}
	if err := s.push(); err != nil {
		return err
	}
	s.printChanges(changes)
	fmt.Fprintln(s.Out, "pushed")
	return nil
}

// Pull fetches the remote and mirrors the staging repo into the live
// files. Without force it refuses when the live files hold changes that
// were never pushed, since mirroring would discard them.
func (s *Syncer) Pull(force bool) error {
	if err := s.prepare(); err != nil {
		return err
	}
	if !force {
		local, _, err := diff(s.live(), s.repo())
		if err != nil {
			return err
		}
		if len(local) > 0 {
			var b strings.Builder
			for _, c := range local {
				fmt.Fprintf(&b, "\n  %s", c)
			}
			return fmt.Errorf("local changes not yet pushed:%s\nrun uc sync push first, or uc sync pull --force to discard them", b.String())
		}
	}

	if !s.hasUpstream() {
		tracked, err := s.trackRemoteBranch()
		if err != nil {
			return err
		}
		if !tracked {
			fmt.Fprintln(s.Out, "remote is empty — nothing to pull")
			return nil
		}
	}
	if s.hasHead() {
		// --rebase replays a commit left by a rejected push on top of the
		// remote's, so non-conflicting edits from two machines both survive.
		out, err := s.git("pull", "-q", "--rebase")
		if err != nil {
			_, _ = s.git("rebase", "--abort")
			if errors.Is(err, context.DeadlineExceeded) {
				return &gitError{doing: "pulling", err: err}
			}
			msg := fmt.Sprintf("sync repo has diverged — resolve manually in %s, then run uc sync pull --force", s.Dir)
			if out != "" {
				msg += "\n  " + strings.ReplaceAll(out, "\n", "\n  ")
			}
			return errors.New(msg)
		}
		if err := s.convertLegacyRepo(); err != nil {
			return err
		}
	}

	changes, err := mirror(s.repo(), s.live(), s.Warn)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		fmt.Fprintln(s.Out, "already up to date")
	} else {
		s.printChanges(changes)
	}
	if ahead, _, err := s.aheadBehind(); err == nil && ahead > 0 {
		fmt.Fprintln(s.Out, "local commits not yet pushed — run uc sync push")
	}
	return nil
}

// trackRemoteBranch handles a staging repo cloned while the remote was
// still empty: once another machine has pushed, point this repo at that
// branch. With no local commits the branch is simply checked out; with
// some, the local branch is renamed to match and set to track it, ready
// for a rebase. Reports false when the remote still has no branches.
func (s *Syncer) trackRemoteBranch() (bool, error) {
	if _, err := s.gitDoTimeout(quickTimeout, "fetching", "fetch", "-q", "origin"); err != nil {
		return false, err
	}
	out, err := s.gitDo("reading the remote's branches", "for-each-ref", "--format=%(refname:short)", "refs/remotes/origin")
	if err != nil {
		return false, err
	}
	var branches []string
	for ref := range strings.FieldsSeq(out) {
		if ref != "origin/HEAD" && ref != "origin" {
			branches = append(branches, strings.TrimPrefix(ref, "origin/"))
		}
	}
	if len(branches) == 0 {
		return false, nil
	}
	branch := branches[0]
	for _, pref := range []string{"main", "master"} {
		if slices.Contains(branches, pref) {
			branch = pref
			break
		}
	}

	if !s.hasHead() {
		_, err := s.gitDo("checking out "+branch, "checkout", "-q", "-B", branch, "--track", "origin/"+branch)
		return err == nil, err
	}
	if _, err := s.gitDo("setting up "+branch, "branch", "-M", branch); err != nil {
		return false, err
	}
	if _, err := s.gitDo("setting up "+branch, "branch", "-q", "--set-upstream-to", "origin/"+branch); err != nil {
		return false, err
	}
	return true, nil
}

// Status prints the remote, any unpushed local changes, and how the
// staging repo compares with the remote.
func (s *Syncer) Status() error {
	if err := s.prepare(); err != nil {
		return err
	}
	url, err := s.gitDo("reading the remote", "remote", "get-url", "origin")
	if err != nil {
		return err
	}
	fmt.Fprintf(s.Out, "remote: %s\n", url)

	// Offline status is still useful: warn and compare against the last
	// fetched state.
	if _, err := s.gitDoTimeout(quickTimeout, "fetching", "fetch", "-q", "origin"); err != nil {
		fmt.Fprintf(s.Warn, "uc: warning: could not reach the remote, showing the last fetched state\n")
	}

	local, _, err := diff(s.live(), s.repo())
	if err != nil {
		return err
	}
	if len(local) == 0 {
		fmt.Fprintln(s.Out, "no local changes")
	} else {
		fmt.Fprintln(s.Out, "local changes not yet pushed:")
		for _, c := range local {
			fmt.Fprintf(s.Out, "  %s\n", c)
		}
	}

	if !s.hasUpstream() {
		fmt.Fprintln(s.Out, "nothing published yet — run uc sync push")
		return nil
	}
	ahead, behind, err := s.aheadBehind()
	if err != nil {
		return err
	}
	if ahead == 0 && behind == 0 {
		fmt.Fprintln(s.Out, "up to date with the remote")
	} else {
		fmt.Fprintf(s.Out, "%d commit(s) to push, %d to pull\n", ahead, behind)
	}
	return nil
}
