package main

import (
	"fmt"
	"strconv"
	"strings"
)

// usageError is a mistake in how a command was invoked. main prints msg,
// the command's usage block, and a pointer to help, then exits 2. cmd is
// "" for a top-level mistake (an unknown option).
type usageError struct {
	cmd string
	msg string
}

func (e *usageError) Error() string { return e.msg }

func usagef(cmd, format string, a ...any) error {
	return &usageError{cmd: cmd, msg: fmt.Sprintf(format, a...)}
}

// flagSpec lists a command's flags: name -> whether it takes a value.
type flagSpec map[string]bool

// isFlag reports whether a looks like an option: a leading '-' and more
// after it. A bare "-" is a positional (stdin/stdout for import/export).
func isFlag(a string) bool {
	return len(a) > 1 && a[0] == '-'
}

// splitFlags separates flags from positionals. Flags may appear anywhere;
// a value flag takes the next argument, or "--flag=value". A literal "--"
// ends flag parsing and everything after it is positional. An unknown
// flag, a value flag with no value, a value given to a plain flag, or a
// flag given twice is a usage error.
func splitFlags(cmd string, args []string, spec flagSpec) (flags map[string]string, pos []string, err error) {
	flags = map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if !isFlag(a) {
			pos = append(pos, a)
			continue
		}
		name, value, hasValue := strings.Cut(a, "=")
		takesValue, known := spec[name]
		switch {
		case !known:
			return nil, nil, usagef(cmd, "unknown option '%s'", name)
		case flagSet(flags, name):
			return nil, nil, usagef(cmd, "option '%s' given more than once", name)
		case !takesValue && hasValue:
			return nil, nil, usagef(cmd, "option '%s' takes no value", name)
		case takesValue && !hasValue:
			if i+1 >= len(args) {
				return nil, nil, usagef(cmd, "option '%s' needs a value", name)
			}
			i++
			value = args[i]
		}
		flags[name] = value
	}
	return flags, pos, nil
}

func flagSet(flags map[string]string, name string) bool {
	_, ok := flags[name]
	return ok
}

// positiveInt parses a count or index typed for label ("history",
// "history run"), rejecting anything that isn't a whole number above 0.
func positiveInt(cmd, label, arg string) (int, error) {
	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 {
		return 0, usagef(cmd, "'%s' expects a number, got '%s'", label, arg)
	}
	return n, nil
}

// wantArgs returns a usage error unless min <= len(pos) <= max.
func wantArgs(cmd string, pos []string, min, max int) error {
	switch {
	case len(pos) < min:
		return usagef(cmd, "missing argument")
	case len(pos) > max:
		return usagef(cmd, "unexpected argument '%s'", pos[max])
	}
	return nil
}

// parseAliasAdd parses the arguments after `uc alias add` under the strict
// rule: uc's own options go before the command, and everything from the
// first command word onward belongs to the command.
//
//	uc alias add <name> [--desc <text>] [--] <command>...
//
// literal reports whether "--" was given (the user was explicit, so no
// note about a "--desc" inside the command is needed).
func parseAliasAdd(args []string) (name string, words []string, desc string, literal bool, err error) {
	const cmd = "alias"
	if len(args) == 0 {
		return "", nil, "", false, usagef(cmd, "missing argument")
	}
	name, rest := args[0], args[1:]
	if isFlag(name) {
		return "", nil, "", false, usagef(cmd, "unknown option '%s'", name)
	}
	if len(rest) > 0 && rest[0] == "--desc" {
		if len(rest) < 2 {
			return "", nil, "", false, usagef(cmd, "option '--desc' needs a value")
		}
		desc, rest = rest[1], rest[2:]
		if len(rest) > 0 && rest[0] == "--desc" {
			return "", nil, "", false, usagef(cmd, "option '--desc' given more than once")
		}
	}
	switch {
	case len(rest) > 0 && rest[0] == "--":
		literal, rest = true, rest[1:]
	case len(rest) > 0 && isFlag(rest[0]):
		return "", nil, "", false, usagef(cmd, "unknown option '%s'", rest[0])
	}
	if len(rest) == 0 {
		return "", nil, "", false, usagef(cmd, "missing argument")
	}
	return name, rest, desc, literal, nil
}
