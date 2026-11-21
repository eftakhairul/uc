// Package names holds the rules for script, alias, and function names, and
// for making registered metadata safe to print. It is a leaf package (only
// reserved below it) so registry, store, and bundle can all share it.
package names

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/eftakhairul/uc/internal/reserved"
)

// Exts is the order in which extensions are tried when a bare name is
// resolved to a script file. Order matters: first match wins. "" is the
// extensionless case.
var Exts = []string{"", ".sh", ".py", ".js", ".rb", ".pl"}

// MaxLen is the longest name ValidName accepts, in bytes.
const MaxLen = 64

// MaxMetaRunes caps a cleaned metadata value (desc, usage, example).
const MaxMetaRunes = 200

var validRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*$`)

// BareName strips a known script extension from a filename, if present —
// the name a registered script is invoked by.
func BareName(filename string) string {
	ext := filepath.Ext(filename)
	if slices.Contains(Exts[1:], ext) { // skip "" (no-extension case)
		return strings.TrimSuffix(filename, ext)
	}
	return filename
}

// SafeName reports whether name can be looked up without escaping the
// scripts directory: non-empty, not starting with "." (which also rules out
// "." and ".." and dotfiles such as .DS_Store), no path separator of either
// kind regardless of GOOS, no NUL, and equal to its own base name.
//
// Deliberately looser than ValidName so entries created by older versions
// of uc stay reachable (and removable).
func SafeName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || strings.ContainsAny(name, "/\\\x00") {
		return false
	}
	return name == filepath.Base(name)
}

// ValidName is the rule for creating a script, alias, or function name:
// letters, digits, '.', '_' and '-', starting with a letter, digit or '_',
// at most MaxLen bytes, and — once a known script extension is stripped —
// not a reserved uc command.
func ValidName(name string) error {
	if !validRe.MatchString(name) {
		return fmt.Errorf("invalid name '%s' — use letters, digits, '.', '_' or '-', and start with a letter, digit or '_'", name)
	}
	if len(name) > MaxLen {
		return fmt.Errorf("invalid name '%s' — use at most %d characters", name, MaxLen)
	}
	if bare := BareName(name); reserved.Is(bare) {
		return fmt.Errorf("'%s' is a reserved uc command", bare)
	}
	return nil
}

// isControl reports whether r is a C0 or C1 control character, or DEL.
func isControl(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f)
}

// CleanMeta makes a metadata string safe to print: control characters
// (U+0000–U+001F, U+007F–U+009F) are removed — so an ANSI escape sequence
// can't recolour, hide, or rewrite terminal output — and the result is cut
// to MaxMetaRunes runes. Invalid UTF-8 bytes are dropped too.
func CleanMeta(s string) string {
	var b strings.Builder
	n := 0
	for i, r := range s {
		if r == utf8.RuneError {
			if _, size := utf8.DecodeRuneInString(s[i:]); size <= 1 {
				continue
			}
		}
		if isControl(r) {
			continue
		}
		if n == MaxMetaRunes {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// EscapeControl makes text that will be run (an alias command, a function
// body) safe to print without altering what it says: control characters
// other than newline and tab are shown as Go-style escapes (\x1b, \u0085)
// instead of being interpreted by the terminal.
func EscapeControl(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x80 && isControl(r):
			fmt.Fprintf(&b, `\x%02x`, r)
		case isControl(r):
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
