//go:build !windows

package executor

import (
	"strings"
	"syscall"
	"testing"
)

func TestExplainExecError(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{syscall.ENOEXEC, "cannot run '/s/x': it has no shebang line (for example #!/usr/bin/env bash)"},
		{syscall.EACCES, "cannot run '/s/x': it is not executable — run chmod +x on it, or add it again"},
		{syscall.ENOENT, "cannot run '/s/x': no such file or directory"},
	} {
		if got := explainExecError("/s/x", c.err).Error(); got != c.want {
			t.Errorf("explain(%v) = %q, want %q", c.err, got, c.want)
		}
		if strings.Contains(explainExecError("/s/x", c.err).Error(), "exec ") {
			t.Error("internal exec wording leaked")
		}
	}
}
