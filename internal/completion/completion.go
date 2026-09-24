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

// Pair is a completion candidate together with its description, for shells
// whose completion menu renders one.
type Pair struct {
	Name string
	Desc string
}

// Candidates returns registered script/alias/function names + non-hidden
// reserved subcommand names, filtered to those with the given prefix and
// ranked by frecency descending (then alphabetically), per architecture
// §9.1.
func Candidates(reg *registry.Registry, entries []history.Entry, weights ranker.Weights, prefix string) ([]string, error) {
	pairs, err := DescribedCandidates(reg, entries, weights, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(pairs))
	for i, p := range pairs {
		out[i] = p.Name
	}
	return out, nil
}

// DescribedCandidates is Candidates plus each candidate's description, for
// shells whose completion menu renders one (fish). Reserved subcommand names
// carry no description: their help text is multi-line prose, not a one-liner.
func DescribedCandidates(reg *registry.Registry, entries []history.Entry, weights ranker.Weights, prefix string) ([]Pair, error) {
	items, err := reg.List()
	if err != nil {
		return nil, err
	}

	all := make([]string, 0, len(items)+len(reserved.Names))
	descs := make(map[string]string, len(items))
	for _, it := range items {
		all = append(all, it.Name)
		descs[it.Name] = it.Desc
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
	out := make([]Pair, len(ranked))
	for i, r := range ranked {
		out[i] = Pair{Name: r.Name, Desc: descs[r.Name]}
	}
	return out, nil
}

// Script returns the thin bash/zsh/fish completion script for the given
// shell. All call back into `uc __complete` (architecture §9.1) and forward
// the result to the shell's native completion machinery.
func Script(shell string) (string, error) {
	switch shell {
	case "bash":
		return bashScript, nil
	case "zsh":
		return zshScript, nil
	case "fish":
		return fishScript, nil
	default:
		return "", fmt.Errorf("unsupported shell %q (want bash, zsh or fish)", shell)
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

// fishScript is installed by writing it to
// ~/.config/fish/completions/uc.fish, where fish autoloads it lazily — no
// eval at shell startup. -f suppresses fish's default filename completion,
// the -n guard completes only the first argument (args after `uc <name>`
// belong to the entry being run), and -k preserves emission order so the
// frecency ranking survives fish's otherwise-alphabetical sort. Candidate
// lines are "name<TAB>description"; fish renders the part after the tab as
// the pager description.
//
// `commandline -o` (--tokenize) is deprecated in fish 4 in favour of
// --tokens-expanded, but kept here: --tokens-expanded only exists from fish
// 3.7, and the deprecated flag still works in every release in the wild
// (including Debian stable's fish 3.6).
const fishScript = `# uc fish completion
complete -c uc -f -n "test (count (commandline -opc)) -eq 1" \
    -k -a "(uc __complete --describe (commandline -ct))"
`
