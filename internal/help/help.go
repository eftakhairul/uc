// Package help implements `uc help` routing (architecture §3.5): static
// built-in text for reserved subcommands, and shared rendering for a
// resolved script/alias/function's desc/usage/examples metadata.
package help

import (
	"fmt"
	"strings"
)

// Static returns built-in help text for a reserved subcommand, and whether
// one exists for it.
func Static(name string) (string, bool) {
	text, ok := staticText[name]
	return text, ok
}

var staticText = map[string]string{
	"list": `uc list, uc ls

Enumerate every registered script, alias, and function together, with its
kind and description (if any), sorted by name.
`,
	"ls": `uc list, uc ls

Enumerate every registered script, alias, and function together, with its
kind and description (if any), sorted by name.
`,
	"add": `uc add <path> [name]

Register a script: copies <path> into $UC_HOME/scripts/<name or
basename(path)> and sets its executable bit.
`,
	"remove": `uc remove <name>, uc rm <name>

Resolve <name> (script, alias, or function) and delete/unregister it.
`,
	"rm": `uc remove <name>, uc rm <name>

Resolve <name> (script, alias, or function) and delete/unregister it.
`,
	"which": `uc which <name>

Resolve <name> and print what it resolves to and which kind it is: a
script's absolute path, an alias's command text, or a function's body.
`,
	"edit": `uc edit <name>

Scripts: opens the file in $EDITOR. Aliases and functions: temp-file
round-trip of the command/body through $EDITOR (same as 'uc alias edit' /
'uc function edit').
`,
	"completion": `uc completion <bash|zsh>

Print a shell completion script to stdout, for 'eval "$(uc completion bash)"'.
`,
	"history": `uc history [n]
uc history run <n>

'uc history [n]' prints the last n invocations (default/max: the
configured history_size, 25 out of the box), most recent first, with
relative timestamps and kind.

'uc history run <n>' re-runs the nth entry (1 = most recent) with its
original args.
`,
	"alias": `uc alias add <name> <command...> [--desc "..."]
uc alias remove <name>
uc alias list
uc alias edit <name>

An alias maps a name to arbitrary shell command text — the same idea as a
bash alias (e.g. alias gs='git status'). It isn't required to be a
registered uc script. Invocation args are automatically appended at the
end, e.g. 'uc alias add gs git status' then 'uc gs -s' runs 'git status -s'.
'uc alias edit' opens the command text in $EDITOR; --desc sets the
description shown in 'uc list' and the picker.
`,
	"function": `uc function add <name> <body> [--desc "..."]
uc function remove <name>
uc function list
uc function edit <name>

A function is a named, inline shell snippet run via 'bash -c'. Useful for
chaining multiple 'uc' commands together, e.g.
'uc build && uc test && uc deploy'.
`,
	"help": `uc help [subcommand|name]

With no argument, print the overall usage. With an argument, print help for
a reserved subcommand, or a script/alias/function's @desc/@usage/@example
metadata. 'uc <name> --help' (sole argument) is a shortcut for
'uc help <name>'.
`,
	"version": `uc version

Print the version string.
`,
}

// RenderMeta renders a script/alias/function's metadata in the shared
// shape used for all three kinds (architecture §3.5). A name with no
// metadata at all still gets a minimal rendering.
func RenderMeta(name, desc, usage string, examples []string) string {
	var b strings.Builder
	if desc != "" {
		fmt.Fprintf(&b, "%s — %s\n", name, desc)
	} else {
		fmt.Fprintf(&b, "%s — no description available\n", name)
	}
	if usage != "" {
		fmt.Fprintf(&b, "\nUsage:\n  %s\n", usage)
	}
	if len(examples) > 0 {
		b.WriteString("\nExamples:\n")
		for _, ex := range examples {
			fmt.Fprintf(&b, "  %s\n", ex)
		}
	}
	return b.String()
}
