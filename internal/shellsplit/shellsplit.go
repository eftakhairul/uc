// Package shellsplit splits a command line into words the way a POSIX shell
// would for quoting purposes — single quotes, double quotes, and backslash
// escapes — without any expansion. Used to split the editor command.
//
// Backslash handling is deliberately narrower than a shell's, so paths and
// regexes typed with backslashes survive: outside quotes a backslash only
// escapes whitespace, a quote, or another backslash, and on Windows (where
// backslash is the path separator) it is always literal outside quotes.
package shellsplit

import (
	"errors"
	"runtime"
	"strings"
)

// ErrUnclosedQuote is returned for input that ends inside a quote.
var ErrUnclosedQuote = errors.New("unclosed quote")

// Split breaks s into words using the current platform's backslash rules.
// Inside single quotes everything is literal. Inside double quotes a
// backslash escapes only \, ", $, and ` (only " on Windows); elsewhere it is
// kept. Outside quotes a backslash escapes whitespace, a quote, or a
// backslash (never on Windows) and is otherwise kept, so `C:\Windows\x.exe`
// and `a\.b` pass through unchanged. Empty quotes produce an empty word.
func Split(s string) ([]string, error) {
	return split(s, runtime.GOOS == "windows")
}

func split(s string, windows bool) ([]string, error) {
	dqEscapable := `\"$` + "`"
	if windows {
		dqEscapable = `"`
	}
	var (
		words   []string
		cur     strings.Builder
		inWord  bool
		runes   = []rune(s)
		quoteCh rune // 0, '\'', or '"'
	)
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch quoteCh {
		case '\'':
			if c == '\'' {
				quoteCh = 0
			} else {
				cur.WriteRune(c)
			}
			continue
		case '"':
			switch {
			case c == '"':
				quoteCh = 0
			case c == '\\' && i+1 < len(runes) && strings.ContainsRune(dqEscapable, runes[i+1]):
				i++
				cur.WriteRune(runes[i])
			default:
				cur.WriteRune(c)
			}
			continue
		}
		switch c {
		case ' ', '\t', '\n':
			flush()
		case '\'', '"':
			quoteCh = c
			inWord = true
		case '\\':
			inWord = true
			if !windows && i+1 < len(runes) && strings.ContainsRune(" \t\n'\"\\", runes[i+1]) {
				i++
				cur.WriteRune(runes[i])
			} else {
				cur.WriteRune(c)
			}
		default:
			inWord = true
			cur.WriteRune(c)
		}
	}
	if quoteCh != 0 {
		return nil, ErrUnclosedQuote
	}
	flush()
	return words, nil
}
