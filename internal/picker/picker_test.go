package picker

import "testing"

func TestFuzzyMatch(t *testing.T) {
	cases := []struct {
		s, pattern string
		want       bool
	}{
		{"killport", "", true},
		{"killport", "kp", true},
		{"killport", "KLP", true},
		{"killport", "pk", false},
		{"killport", "killports", false},
		{"déploy", "dép", true},
		{"déploy", "épy", true},
		{"deploy", "é", false},
	}
	for _, c := range cases {
		if got := fuzzyMatch(c.s, c.pattern); got != c.want {
			t.Errorf("fuzzyMatch(%q, %q) = %v, want %v", c.s, c.pattern, got, c.want)
		}
	}
}

func TestParseArgLine(t *testing.T) {
	name, args := parseArgLine("deploy --env=prod  --verbose")
	if name != "deploy" || len(args) != 2 || args[0] != "--env=prod" || args[1] != "--verbose" {
		t.Errorf("got %q %v", name, args)
	}
	name, args = parseArgLine("  ")
	if name != "" || args != nil {
		t.Errorf("got %q %v, want empty", name, args)
	}
}
