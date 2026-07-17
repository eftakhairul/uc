// Package completion implements `uc completion <shell>` (emitting thin
// shell scripts) and the ranked-candidate logic behind `uc __complete`
// (architecture §9.1).
package completion

import (
	"fmt"
	"strings"
	"time"

	"github.com/eftakhairul/uc/internal/history"
	"github.com/eftakhairul/uc/internal/ranker"
	"github.com/eftakhairul/uc/internal/registry"
	"github.com/eftakhairul/uc/internal/reserved"
)

// Candidates returns registered script/alias/function names + non-hidden
// reserved subcommand names, filtered to those with the given prefix and
// ranked by frecency descending (then alphabetically), per architecture
// §9.1.
func Candidates(reg *registry.Registry, entries []history.Entry, weights ranker.Weights, prefix string) ([]string, error) {
	items, err := reg.List()
	if err != nil {
		return nil, err
	}

	all := make([]string, 0, len(items)+len(reserved.Names))
	for _, it := range items {
		all = append(all, it.Name)
	}
	for _, n := range reserved.Names {
		if reserved.Hidden[n] {
			continue
		}
		all = append(all, n)
	}

	filtered := make([]string, 0, len(all))
	for _, name := range all {
		if strings.HasPrefix(name, prefix) {
			filtered = append(filtered, name)
		}
	}

	ranked := ranker.Rank(filtered, entries, time.Now(), weights)
	out := make([]string, len(ranked))
	for i, r := range ranked {
		out[i] = r.Name
	}
	return out, nil
}

// Script returns the thin bash/zsh completion script for the given shell.
// Both call back into `uc __complete` (architecture §9.1) and forward the
// result to the shell's native completion machinery.
func Script(shell string) (string, error) {
	switch shell {
	case "bash":
		return bashScript, nil
	case "zsh":
		return zshScript, nil
	default:
		return "", fmt.Errorf("unsupported shell %q (want bash or zsh)", shell)
	}
}

const bashScript = `# uc bash completion
_uc_complete() {
    local cur
    cur="${COMP_WORDS[COMP_CWORD]}"
    if [ "$COMP_CWORD" -eq 1 ]; then
        COMPREPLY=( $(uc __complete "$cur") )
    fi
}
complete -F _uc_complete uc
`

const zshScript = `# uc zsh completion
_uc_complete() {
    local -a candidates
    if [ "$CURRENT" -eq 2 ]; then
        candidates=(${(f)"$(uc __complete "${words[2]}")"})
        compadd -a candidates
    fi
}
compdef _uc_complete uc
`
