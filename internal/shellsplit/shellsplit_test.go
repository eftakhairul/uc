package shellsplit

import (
	"errors"
	"strings"
	"testing"
)

func TestSplit(t *testing.T) {
	for in, want := range map[string][]string{
		"":                               nil,
		"   ":                            nil,
		"commit -m two words":            {"commit", "-m", "two", "words"},
		`commit -m "two words"`:          {"commit", "-m", "two words"},
		`commit -m 'two words'`:          {"commit", "-m", "two words"},
		`a "b c"d e`:                     {"a", "b cd", "e"},
		`echo 'it''s'`:                   {"echo", "its"},
		`say "she said \"hi\""`:          {"say", `she said "hi"`},
		`path a\ b`:                      {"path", "a b"},
		`"$HOME" '$HOME'`:                {"$HOME", "$HOME"},
		`"a\nb"`:                         {`a\nb`},
		`'a\nb'`:                         {`a\nb`},
		`x ""`:                           {"x", ""},
		`trailing\`:                      {`trailing\`},
		"\"/Applications/My Editor\" -w": {"/Applications/My Editor", "-w"},
		"tab\tsep\nnl":                   {"tab", "sep", "nl"},
	} {
		got, err := Split(in)
		if err != nil {
			t.Errorf("Split(%q): %v", in, err)
			continue
		}
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("Split(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitUnclosed(t *testing.T) {
	for _, in := range []string{`a "b`, `a 'b`, `"`, `x "y \"`} {
		if _, err := Split(in); !errors.Is(err, ErrUnclosedQuote) {
			t.Errorf("Split(%q) err = %v, want ErrUnclosedQuote", in, err)
		}
	}
}
