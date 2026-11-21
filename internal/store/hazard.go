package store

import (
	"fmt"
	"strings"
)

// AppendHazard reports why appending `"$@"` to command may misbehave, or
// "" when no hazard is detected.
//
// The executor appends the user's invocation args as `"$@"` to the end of
// the command text (architecture §3.2b), which silently breaks when the
// text ends in a control operator (the args become their own command) or
// contains a comment (the args are commented out). This is a heuristic for
// warning purposes, not a shell parser: it tracks single/double quotes only.
func AppendHazard(command string) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return ""
	}

	// Checked before the trailing-operator rules: a comment swallows
	// everything after it, including any trailing operator.
	if hasUnquotedComment(trimmed) {
		return "args are appended after a # comment and will be ignored"
	}

	// Longest operators first so "&&" isn't reported as "&".
	for _, op := range []string{"&&", "||", "&", ";", "|"} {
		if !strings.HasSuffix(trimmed, op) || isQuotedTail(trimmed) {
			continue
		}
		if op == "|" {
			return fmt.Sprintf("args would be run as the command on the right of the trailing %q", op)
		}
		return fmt.Sprintf("args would run as a separate command after the trailing %q", op)
	}
	return ""
}

// hasUnquotedComment reports whether s contains a `#` that bash would treat
// as starting a comment: outside quotes, and at the start of a word.
func hasUnquotedComment(s string) bool {
	var inSingle, inDouble bool
	prevIsSpace := true
	for _, c := range s {
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '#' && !inSingle && !inDouble && prevIsSpace:
			return true
		}
		prevIsSpace = c == ' ' || c == '\t' || c == '\n'
	}
	return false
}

// isQuotedTail reports whether s ends inside an unclosed quote, in which
// case a trailing operator character is literal text rather than an
// operator (e.g. `echo "tom & jerry`).
func isQuotedTail(s string) bool {
	var inSingle, inDouble bool
	for _, c := range s {
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		}
	}
	return inSingle || inDouble
}
