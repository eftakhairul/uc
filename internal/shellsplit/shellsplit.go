// Package shellsplit splits a command line into words the way a POSIX shell
// would for quoting purposes — single quotes, double quotes, and backslash
// escapes — without any expansion. Used for the picker's argument line and
// the editor command.
package shellsplit

import (
	"errors"
	"strings"
)

// ErrUnclosedQuote is returned for input that ends inside a quote.
var ErrUnclosedQuote = errors.New("unclosed quote")

// Split breaks s into words. Inside single quotes everything is literal.
// Inside double quotes a backslash escapes only \, ", $, and `; elsewhere
// it is kept. Outside quotes a backslash escapes the next character, and a
// trailing backslash is kept as itself. Empty quotes produce an empty word.
func Split(s string) ([]string, error) {
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
			case c == '\\' && i+1 < len(runes) && strings.ContainsRune(`\"$`+"`", runes[i+1]):
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
			if i+1 < len(runes) {
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
