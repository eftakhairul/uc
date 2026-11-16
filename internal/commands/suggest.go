package commands

import (
	"sort"
	"strings"

	"github.com/eftakhairul/uc/internal/reserved"
)

// maxSuggestions caps how many similar names a not-found error lists.
const maxSuggestions = 3

// suggest returns up to three registered or reserved names close to name:
// starting with name (when it is at least 2 characters), or within edit
// distance 2 (1 for names shorter than 4). Like git, only the best-scoring
// candidates are kept — a prefix match beats any edit distance, and
// distance 1 beats distance 2 — sorted by name. Errors while listing just
// mean no suggestions; they must never mask the not-found error being
// decorated.
func (r *Runner) suggest(name string) []string {
	if name == "" {
		return nil
	}
	candidates := map[string]bool{}
	if items, err := r.Reg.List(); err == nil {
		for _, it := range items {
			candidates[it.Name] = true
		}
	}
	for _, n := range reserved.Names {
		if !reserved.Hidden[n] {
			candidates[n] = true
		}
	}

	limit := 2
	if len([]rune(name)) < 4 {
		limit = 1
	}
	best := limit + 1
	scores := map[string]int{}
	for c := range candidates {
		if c == name {
			continue
		}
		score := levenshtein(name, c)
		if len(name) >= 2 && strings.HasPrefix(c, name) {
			score = 0
		}
		if score <= limit {
			scores[c] = score
			best = min(best, score)
		}
	}
	var out []string
	for c, score := range scores {
		if score == best {
			out = append(out, c)
		}
	}
	sort.Strings(out)
	if len(out) > maxSuggestions {
		out = out[:maxSuggestions]
	}
	return out
}

// levenshtein is the edit distance between a and b, on runes.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
