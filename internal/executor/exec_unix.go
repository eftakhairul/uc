//go:build !windows

package executor

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// exec1 replaces the current process image with bin (architecture §3.3).
func exec1(bin string, argv []string) error {
	env := os.Environ()
	if err := syscall.Exec(bin, argv, env); err != nil {
		return err
	}
	return nil // unreachable on success
}

// explainExecError turns an exec failure on a script into advice about the
// script file.
func explainExecError(path string, err error) error {
	switch {
	case errors.Is(err, syscall.ENOEXEC):
		return noShebang(path)
	case errors.Is(err, syscall.EACCES):
		return notExecutable(path)
	default:
		return fmt.Errorf("cannot run '%s': %v", path, err)
	}
}
