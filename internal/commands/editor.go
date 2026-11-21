package commands

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"github.com/eftakhairul/uc/internal/shellsplit"
)

// errEditorFailed means the editor ran and exited non-zero: the user
// aborted, so nothing is saved.
var errEditorFailed = errors.New("editor exited with an error")

// editorArgv builds the editor command line for path from settings.json's
// editor, $EDITOR, or vi. The command is split with shell quoting rules,
// so a path with spaces can be quoted.
func (r *Runner) editorArgv(path string) ([]string, error) {
	cmd := r.Cfg.EditorCommand()
	words, err := shellsplit.Split(cmd)
	if err != nil {
		return nil, fmt.Errorf("cannot parse the editor command '%s': %v", cmd, err)
	}
	if len(words) == 0 {
		return nil, errors.New(`the editor command is empty — set "editor" in settings.json, or $EDITOR`)
	}
	if _, err := exec.LookPath(words[0]); err != nil {
		return nil, fmt.Errorf(`editor '%s' not found — set "editor" in settings.json, or $EDITOR`, words[0])
	}
	return append(words, path), nil
}

// runEditor opens the editor on path as a child process, wiring through
// the terminal, and waits for it. Used by the flows that need control back
// afterward to validate or persist the result — unlike `uc edit` on a
// script, which replaces the process outright. A non-zero exit is
// errEditorFailed.
func (r *Runner) runEditor(path string) error {
	argv, err := r.editorArgv(path)
	if err != nil {
		return err
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		if _, ok := errors.AsType[*exec.ExitError](err); ok {
			return errEditorFailed
		}
		return fmt.Errorf("cannot start editor '%s': %s", argv[0], reason(err))
	}
	return nil
}

// editViaTempFile writes initial to a temp file, opens the editor on it,
// and on clean exit reads the edited content back (deleting the temp
// file). A non-zero editor exit aborts, and nothing should be saved
// (architecture §6, decision 17).
func (r *Runner) editViaTempFile(pattern, initial string) (string, error) {
	tmp, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(initial); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp file: %w", err)
	}

	if err := r.runEditor(tmpPath); err != nil {
		if errors.Is(err, errEditorFailed) {
			return "", errors.New("editor exited with an error, nothing saved")
		}
		return "", err
	}

	edited, err := os.ReadFile(tmpPath)
	if err != nil {
		return "", fmt.Errorf("read edited content: %w", err)
	}
	return string(edited), nil
}
