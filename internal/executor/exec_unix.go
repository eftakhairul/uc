//go:build !windows

package executor

import (
	"fmt"
	"os"
	"syscall"
)

// exec1 replaces the current process image with bin (architecture §3.3).
func exec1(bin string, argv []string) error {
	env := os.Environ()
	if err := syscall.Exec(bin, argv, env); err != nil {
		return fmt.Errorf("exec %s: %w", bin, err)
	}
	return nil // unreachable on success
}
