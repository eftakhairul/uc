// Package executor runs resolved scripts via process replacement
// (syscall.Exec) so the invoked script inherits the terminal exactly as if
// it had been run directly (architecture §3.3). Windows has no process
// replacement — there, exec1 falls back to spawning the child with inherited
// stdio and mirroring its exit code (see exec_windows.go).
//
// Running is split in two: Prepare* resolves the interpreter or bash and
// builds argv, and Plan.Exec hands over. Callers record history between the
// two, so a run that could never start isn't recorded.
package executor

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// interpreters maps a script extension to the interpreter binaries that
// can run it, tried in order on $PATH at runtime (architecture §3.3).
// Multiple candidates exist where installations disagree on the binary
// name: python.org and Microsoft Store installs expose "python", while
// unix distributions expose "python3".
var interpreters = map[string][]string{
	".sh": {"bash"},
	".py": {"python3", "python"},
	".js": {"node"},
	".rb": {"ruby"},
	".pl": {"perl"},
}

// lookupInterpreter returns the resolved binary path and the argv[0] name
// for ext, trying each candidate on $PATH in order, and whether ext has a
// registered interpreter at all.
func lookupInterpreter(ext string) (bin, name string, err error) {
	candidates, ok := interpreters[ext]
	if !ok {
		return "", "", errNoInterpreter
	}
	for _, c := range candidates {
		if found, lookErr := exec.LookPath(c); lookErr == nil {
			return found, c, nil
		}
	}
	return "", "", fmt.Errorf("%s not found on PATH", strings.Join(candidates, " or "))
}

// errNoInterpreter signals that an extension has no registered
// interpreter, so the script must be executed directly via its shebang.
var errNoInterpreter = errors.New("no interpreter registered for extension")

// Plan is a ready-to-exec invocation.
type Plan struct {
	Bin  string   // resolved binary
	Argv []string // argv, argv[0] included
	// Path is the script file for a script plan ("" for alias/function),
	// used to explain exec failures in terms of the file the user owns.
	Path string
}

// Prepare resolves how to run the script at path: via its interpreter for
// known extensions, or directly for extensionless scripts (which must have
// their own shebang and exec bit).
func Prepare(path string, args []string) (*Plan, error) {
	bin, interp, err := lookupInterpreter(filepath.Ext(path))
	switch {
	case err == nil:
		return &Plan{Bin: bin, Argv: append([]string{interp, path}, args...), Path: path}, nil
	case errors.Is(err, errNoInterpreter):
		found, lookErr := exec.LookPath(path)
		if lookErr != nil {
			return nil, notExecutable(path)
		}
		return &Plan{Bin: found, Argv: append([]string{path}, args...), Path: path}, nil
	default:
		return nil, fmt.Errorf("cannot run '%s': %v", filepath.Base(path), err)
	}
}

func notExecutable(path string) error {
	return fmt.Errorf("cannot run '%s': it is not executable — run chmod +x on it, or add it again", path)
}

func noShebang(path string) error {
	return fmt.Errorf("cannot run '%s': it has no shebang line (for example #!/usr/bin/env bash)", path)
}

// PrepareFunction builds a bash invocation of body, placing "uc:<name>" as
// bash's $0 so user args land at $1/$2/$@ as expected (architecture §3.2c)
// — bash -c treats its next argument as $0, not $1.
func PrepareFunction(body, name string, args []string) (*Plan, error) {
	return prepareBash(body, body, name, args)
}

// PrepareAlias builds a bash invocation of command with the user's
// invocation args appended at the end via "$@" — the same behavior as
// bash's own alias expansion (e.g. `alias gs='git status'` then `gs -s`
// runs `git status -s`), so unlike a function body, command doesn't need
// to reference $1/$@ itself (architecture §3.2b).
func PrepareAlias(command, name string, args []string) (*Plan, error) {
	return prepareBash(command, command+` "$@"`, name, args)
}

// prepareBash refuses blank text: an empty alias plus the appended "$@"
// would run the user's arguments as a command.
func prepareBash(text, script, name string, args []string) (*Plan, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("'%s' has an empty command — fix it with 'uc edit %s'", name, name)
	}
	bin, err := exec.LookPath("bash")
	if err != nil {
		return nil, fmt.Errorf("cannot run '%s': bash not found on PATH", name)
	}
	return &Plan{Bin: bin, Argv: append([]string{"bash", "-c", script, "uc:" + name}, args...)}, nil
}

// Exec replaces the current process with the plan. It does not return on
// success.
func (p *Plan) Exec() error {
	err := exec1(p.Bin, p.Argv)
	if err == nil || p.Path == "" {
		return err
	}
	return explainExecError(p.Path, err)
}

// Run prepares and execs the script at path. It does not return on
// success.
func Run(path string, args []string) error {
	p, err := Prepare(path, args)
	if err != nil {
		return err
	}
	return p.Exec()
}

// RunFunction prepares and execs a function body. It does not return on
// success.
func RunFunction(body, name string, args []string) error {
	p, err := PrepareFunction(body, name, args)
	if err != nil {
		return err
	}
	return p.Exec()
}

// RunAlias prepares and execs an alias command. It does not return on
// success.
func RunAlias(command, name string, args []string) error {
	p, err := PrepareAlias(command, name, args)
	if err != nil {
		return err
	}
	return p.Exec()
}

// ExecReplace replaces the current process image with argv[0], looked up
// on $PATH, passing argv and the inherited environment. Used for commands
// like `uc edit`, which need process replacement but aren't a registered
// script. argv must be non-empty.
func ExecReplace(argv []string) error {
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("%s not found on PATH", argv[0])
	}
	return exec1(bin, argv)
}
