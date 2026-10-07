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
	"add": `uc add <path|url> [name] [--force]

Register a script: copies <path> into $UC_HOME/scripts/<name or
basename(path)> and sets its executable bit. Fails if the name already
exists or would be ambiguous with another registered script; pass --force
to overwrite anyway.

An http:// or https:// source is downloaded instead (up to 1 MB). The name
defaults to the last segment of the URL path. A gist page URL
(gist.github.com/<user>/<id>) fetches the gist's first file via /raw; it
has no file name to derive from, so pass one explicitly.

Use raw file URLs (e.g. raw.githubusercontent.com/...), not repository
web pages — uc does not inspect what it downloads, and never runs it on
its own. Review a downloaded script before its first run.
`,
	"new": `uc new <name> [--lang sh|py|js|rb|pl]

Scaffold a new script and open it in $EDITOR. The file is created inside
$UC_HOME/scripts/ before the editor opens, so saving it is what registers
it — there's no separate 'uc add' step.

The scaffold is pre-filled with a shebang for the chosen language (default:
sh) and an empty '@desc/@usage/@example' metadata block, which 'uc list',
the picker, and 'uc help <name>' read back.

Quitting the editor without changing the template registers nothing, and a
non-zero editor exit leaves nothing behind either. Use 'uc edit <name>' to
change the script later.
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
	"completion": `uc completion <bash|zsh|fish>

Print a shell completion script to stdout, for 'eval "$(uc completion bash)"'.

Fish autoloads completions instead of eval'ing them, so install it once:

  uc completion fish > ~/.config/fish/completions/uc.fish

The fish script also shows each candidate's description (from a script's
'@desc' tag or an alias/function's --desc) in the completion pager.
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
	"sync": `uc sync init <remote-url>
uc sync push
uc sync pull [--force]
uc sync status

Sync scripts, aliases, and functions across machines through a git remote
you own (use a PRIVATE repo — scripts may contain secrets). uc shells out
to git; there is no hosted backend, and git must be on PATH.

The remote is cloned into $UC_HOME/sync, laid out as:

  scripts/        mirror of $UC_HOME/scripts
  aliases.json    copy of $XDG_CONFIG_HOME/uc/aliases.json
  functions.json  copy of $XDG_CONFIG_HOME/uc/functions.json

history.json and settings.json are per-machine and never synced.

'uc sync init' clones the remote. If it already holds state, that state is
pulled onto this machine; if it's empty, this machine's state is pushed.

'uc sync push' makes the remote match this machine — additions, edits,
and deletions all propagate. 'uc sync pull' makes this machine match the
remote. Pull refuses while this machine has unpushed changes; --force
discards them and adopts the remote's state.

Conflicts: if another machine pushed first, push fails — run 'uc sync
pull', then push again. Edits to different files from both machines are
kept; if both edited the same file, pull stops and asks you to resolve it
with git in $UC_HOME/sync, then run 'uc sync pull --force'.
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
