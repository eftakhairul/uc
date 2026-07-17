package ranker

import (
	"testing"
	"time"

	"github.com/eftakhairul/uc/internal/history"
)

func TestRank_FrequencyAndRecency(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	entries := []history.Entry{
		{Name: "killport", Timestamp: now.Add(-30 * time.Minute)}, // hour bucket
		{Name: "killport", Timestamp: now.Add(-20 * time.Hour)},   // day bucket (older within same name, but MRU wins)
		{Name: "backup", Timestamp: now.Add(-3 * 24 * time.Hour)}, // week bucket
		{Name: "backup", Timestamp: now.Add(-3 * 24 * time.Hour)},
	}
	candidates := []string{"killport", "backup", "never-run"}

	ranked := Rank(candidates, entries, now, DefaultWeights)

	// killport: count=2, most recent use 30m ago -> hour weight (4) => 8
	// backup: count=2, most recent 3d ago -> week weight (2) => 4
	// never-run: 0
	if ranked[0].Name != "killport" || ranked[0].Score != 8 {
		t.Errorf("ranked[0] = %+v, want killport score 8", ranked[0])
	}
	if ranked[1].Name != "backup" || ranked[1].Score != 4 {
		t.Errorf("ranked[1] = %+v, want backup score 4", ranked[1])
	}
	if ranked[2].Name != "never-run" || ranked[2].Score != 0 {
		t.Errorf("ranked[2] = %+v, want never-run score 0", ranked[2])
	}
}

func TestRank_UnscoredSortAlphabetically(t *testing.T) {
	now := time.Now()
	ranked := Rank([]string{"zeta", "alpha", "mid"}, nil, now, DefaultWeights)
	want := []string{"alpha", "mid", "zeta"}
	for i, r := range ranked {
		if r.Name != want[i] {
			t.Errorf("ranked[%d].Name = %q, want %q", i, r.Name, want[i])
		}
		if r.Score != 0 {
			t.Errorf("ranked[%d].Score = %v, want 0", i, r.Score)
		}
	}
}

func TestRank_OlderBucket(t *testing.T) {
	now := time.Now()
	entries := []history.Entry{
		{Name: "ancient", Timestamp: now.Add(-30 * 24 * time.Hour)},
	}
	ranked := Rank([]string{"ancient"}, entries, now, DefaultWeights)
	if ranked[0].Score != 1 { // count 1 * older weight 1
		t.Errorf("got score %v, want 1", ranked[0].Score)
	}
}
