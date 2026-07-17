// Package ranker computes frecency scores from history data (architecture
// §8.4). It performs no I/O of its own: it's a pure function over history
// entries and a candidate name list, which keeps it unit-testable with
// fabricated data.
package ranker

import (
	"sort"
	"time"

	"github.com/eftakhairul/uc/internal/history"
)

// Weights are the recency-bucket multipliers (architecture §8.4 table).
type Weights struct {
	Hour  float64
	Day   float64
	Week  float64
	Older float64
}

// DefaultWeights matches the architecture's suggested bucket scheme.
var DefaultWeights = Weights{Hour: 4, Day: 3, Week: 2, Older: 1}

// Ranked is one candidate with its computed frecency score.
type Ranked struct {
	Name  string
	Score float64
}

// recencyWeight returns the weight for how long ago 'used' was, relative
// to 'now'.
func recencyWeight(used, now time.Time, w Weights) float64 {
	age := now.Sub(used)
	switch {
	case age < time.Hour:
		return w.Hour
	case age < 24*time.Hour:
		return w.Day
	case age < 7*24*time.Hour:
		return w.Week
	default:
		return w.Older
	}
}

// Rank scores each candidate name by frecency — frequency within entries
// multiplied by the recency weight of its most recent use — and returns
// them sorted by score descending, then alphabetically. Candidates with no
// matching entries score 0 and sort after all scored candidates
// (alphabetically among themselves), per architecture §8.4.
func Rank(candidates []string, entries []history.Entry, now time.Time, w Weights) []Ranked {
	type stats struct {
		count      int
		mostRecent time.Time
	}
	byName := make(map[string]*stats)
	for _, e := range entries {
		s, ok := byName[e.Name]
		if !ok {
			s = &stats{}
			byName[e.Name] = s
		}
		s.count++
		if e.Timestamp.After(s.mostRecent) {
			s.mostRecent = e.Timestamp
		}
	}

	ranked := make([]Ranked, 0, len(candidates))
	for _, name := range candidates {
		score := 0.0
		if s, ok := byName[name]; ok {
			score = float64(s.count) * recencyWeight(s.mostRecent, now, w)
		}
		ranked = append(ranked, Ranked{Name: name, Score: score})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].Score != ranked[j].Score {
			return ranked[i].Score > ranked[j].Score
		}
		return ranked[i].Name < ranked[j].Name
	})

	return ranked
}
