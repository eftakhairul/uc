//go:build windows

package executor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
)

// exec1 approximates process replacement on Windows, where syscall.Exec is
// an unsupported stub (always EWINDOWS): spawn the child with inherited
// stdio and environment, wait for it, and exit with its exit code. The
// terminal, exit code, and Ctrl+C behavior match the unix path; only the
// intermediate uc process lingers until the child finishes.
func exec1(bin string, argv []string) error {
	code, err := runChild(bin, argv, os.Environ())
	if err != nil {
		return err
	}
	os.Exit(code)
	return nil // unreachable
}

// runChild runs bin with the exact argv and env, stdio inherited, and
// returns the child's exit code. Split from exec1 so the wait/exit-code
// logic is testable without exiting the test process.
func runChild(bin string, argv []string, env []string) (int, error) {
	// exec.Cmd is built directly rather than via exec.Command so argv[0]
	// is preserved as given — runBash relies on placing "uc:<name>" in a
	// specific argv slot (architecture §3.2c).
	cmd := &exec.Cmd{
		Path:   bin,
		Args:   argv,
		Env:    env,
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}

	// Ctrl+C is delivered to every process attached to the console; ignore
	// it in uc so only the child decides how to handle the interrupt, same
	// as if it had been run directly.
	signal.Ignore(os.Interrupt)

	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return exitErr.ExitCode(), nil
		}
		return 0, fmt.Errorf("run %s: %w", bin, err)
	}
	return 0, nil
}
