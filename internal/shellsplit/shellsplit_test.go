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
		got, err := split(in, false)
		if err != nil {
			t.Errorf("split(%q): %v", in, err)
			continue
		}
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("split(%q) = %q, want %q", in, got, want)
		}
	}
}

// Backslashes that don't escape whitespace, a quote, or a backslash are
// kept, so Windows paths and regexes in the editor command survive.
func TestSplitKeepsOtherBackslashes(t *testing.T) {
	for in, want := range map[string][]string{
		`grep a\.b`:                 {"grep", `a\.b`},
		`C:\Windows\notepad.exe -w`: {`C:\Windows\notepad.exe`, "-w"},
		`a\\b`:                      {`a\b`},
		`it\'s`:                     {"it's"},
	} {
		got, err := split(in, false)
		if err != nil {
			t.Errorf("split(%q): %v", in, err)
			continue
		}
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("split(%q) = %q, want %q", in, got, want)
		}
	}
}

// On Windows a backslash is a path separator: literal outside quotes, and
// inside double quotes it only escapes a double quote.
func TestSplitWindows(t *testing.T) {
	for in, want := range map[string][]string{
		`C:\Windows\notepad.exe`:             {`C:\Windows\notepad.exe`},
		`\\server\share\f.txt x`:             {`\\server\share\f.txt`, "x"},
		`"C:\Program Files\Editor\e.exe" -w`: {`C:\Program Files\Editor\e.exe`, "-w"},
		`"say \"hi\""`:                       {`say "hi"`},
		`a\ b`:                               {`a\`, "b"},
	} {
		got, err := split(in, true)
		if err != nil {
			t.Errorf("split(%q, windows): %v", in, err)
			continue
		}
		if strings.Join(got, "|") != strings.Join(want, "|") || len(got) != len(want) {
			t.Errorf("split(%q, windows) = %q, want %q", in, got, want)
		}
	}
}

func TestSplitUnclosed(t *testing.T) {
	for _, in := range []string{`a "b`, `a 'b`, `"`, `x "y \"`} {
		if _, err := split(in, false); !errors.Is(err, ErrUnclosedQuote) {
			t.Errorf("Split(%q) err = %v, want ErrUnclosedQuote", in, err)
		}
	}
}
