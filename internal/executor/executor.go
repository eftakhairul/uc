// Package executor runs resolved scripts via process replacement
// (syscall.Exec) so the invoked script inherits the terminal exactly as if
// it had been run directly (architecture §3.3). Windows has no process
// replacement — there, exec1 falls back to spawning the child with inherited
// stdio and mirroring its exit code (see exec_windows.go).
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
	return "", "", fmt.Errorf("interpreter for %q not found on PATH (tried %s)",
		ext, strings.Join(candidates, ", "))
}

// errNoInterpreter signals that an extension has no registered
// interpreter, so the script must be executed directly via its shebang.
var errNoInterpreter = errors.New("no interpreter registered for extension")

// Run replaces the current process image with the script, via its
// interpreter for known extensions or directly for extensionless scripts
// (which must have their own shebang and exec bit).
//
// This function does not return on success — the process is replaced.
// It only returns if resolving the interpreter/binary fails before exec.
func Run(path string, args []string) error {
	ext := filepath.Ext(path)

	var argv0 string
	var argv []string

	bin, interp, err := lookupInterpreter(ext)
	switch {
	case err == nil:
		argv0 = bin
		argv = append([]string{interp, path}, args...)
	case errors.Is(err, errNoInterpreter):
		bin, lookErr := exec.LookPath(path)
		if lookErr != nil {
			return fmt.Errorf("%s is not executable: %w", path, lookErr)
		}
		argv0 = bin
		argv = append([]string{path}, args...)
	default:
		return err
	}

	return exec1(argv0, argv)
}

// RunFunction replaces the current process image with a bash invocation of
// body, placing name as bash's $0 (a harmless placeholder useful for error
// messages inside the body) so user args land at $1/$2/$@ as expected
// (architecture §3.2c) — bash -c treats its next argument as $0, not $1,
// which this call must account for explicitly.
func RunFunction(body, name string, args []string) error {
	return runBash(body, name, args)
}

// RunAlias replaces the current process image with a bash invocation of
// command, with the user's invocation args automatically appended at the
// end via "$@" — the same behavior as bash's own alias expansion (e.g.
// `alias gs='git status'` then `gs -s` runs `git status -s`), so unlike a
// function body, command doesn't need to reference $1/$@ itself
// (architecture §3.2b).
func RunAlias(command, name string, args []string) error {
	return runBash(command+` "$@"`, name, args)
}

func runBash(script, name string, args []string) error {
	bin, err := exec.LookPath("bash")
	if err != nil {
		return fmt.Errorf("bash not found on PATH: %w", err)
	}
	argv := append([]string{"bash", "-c", script, "uc:" + name}, args...)
	return exec1(bin, argv)
}

// ExecReplace replaces the current process image with argv[0], looked up
// on $PATH, passing argv and the inherited environment. Used for commands
// like `uc edit`, which need process replacement but aren't a registered
// script. argv must be non-empty.
func ExecReplace(argv []string) error {
	bin, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("%s not found on PATH: %w", argv[0], err)
	}
	return exec1(bin, argv)
}
