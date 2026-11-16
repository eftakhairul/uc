// Package help implements `uc help` routing (architecture §3.5): the
// git-style overview, static built-in text for reserved subcommands, and
// shared rendering for a resolved script/alias/function's
// desc/usage/examples metadata.
package help

import (
	"fmt"
	"strings"
)

// shortAliases maps a reserved short form to the command whose help it
// shares.
var shortAliases = map[string]string{"ls": "list", "rm": "remove"}

// Static returns built-in help text for a reserved subcommand, and whether
// one exists for it. Every entry opens with a git-style usage block
// ("usage: …" then "   or: …" lines), a blank line, then prose.
func Static(name string) (string, bool) {
	if full, ok := shortAliases[name]; ok {
		name = full
	}
	text, ok := staticText[name]
	return text, ok
}

var staticText = map[string]string{
	"list": `usage: uc list
   or: uc ls

Enumerate every registered script, alias, and function together, with its
kind and description (if any), sorted by name.
`,
	"add": `usage: uc add <path|url> [<name>] [--force] [--insecure]

Register a script: copies <path> into $UC_HOME/scripts/<name or
basename(path)> and makes it executable. Fails if the name already exists
as a script, alias, or function, or would be ambiguous with another
registered script; pass --force to overwrite anyway.

An https:// source is downloaded instead (up to 1 MB). The name defaults
to the last segment of the URL path. A gist page URL
(gist.github.com/<user>/<id>) fetches the gist's first file via /raw; it
has no file name to derive from, so pass one explicitly.

Plain http:// is refused, because anyone on the network path could change
the script in transit; pass --insecure to allow it (and redirects from
https to http). Addresses on this machine (localhost) are always allowed.

Use raw file URLs (e.g. raw.githubusercontent.com/...), not repository
web pages — a URL that returns a web page is refused. uc never runs what
it downloads on its own; review a downloaded script before its first run.

Names use letters, digits, '.', '_' and '-', and start with a letter,
digit or '_'.
`,
	"new": `usage: uc new <name> [--lang sh|py|js|rb|pl]

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
	"remove": `usage: uc remove <name>
   or: uc rm <name>

Resolve <name> (script, alias, or function) and delete/unregister it.
`,
	"which": `usage: uc which <name>

Resolve <name> and print what it resolves to and which kind it is: a
script's absolute path, an alias's command text, or a function's body.
`,
	"edit": `usage: uc edit <name>

Scripts: opens the file in $EDITOR. Aliases and functions: temp-file
round-trip of the command/body through $EDITOR (same as 'uc alias edit' /
'uc function edit'). Saving an empty file changes nothing.
`,
	"completion": `usage: uc completion <bash|zsh|fish>

Print a shell completion script to stdout, for 'eval "$(uc completion bash)"'.

Fish autoloads completions instead of eval'ing them, so install it once:

  uc completion fish > ~/.config/fish/completions/uc.fish

The fish script also shows each candidate's description (from a script's
'@desc' tag or an alias/function's --desc) in the completion pager.
`,
	"history": `usage: uc history [<n>]
   or: uc history run <n>
   or: uc history clear

'uc history [n]' prints the last n invocations (default/max: the
configured history_size, 25 out of the box), most recent first, with
relative timestamps and kind.

'uc history run <n>' re-runs the nth entry (1 = most recent) with its
original args.

History records the arguments you pass, so a token given on the command
line is stored in $UC_HOME/history.json. 'uc history clear' empties it;
set "history_size": 0 in settings.json to turn history off.
`,
	"alias": `usage: uc alias add <name> [--desc <text>] [--] <command>...
   or: uc alias remove <name>
   or: uc alias list
   or: uc alias edit <name>

An alias maps a name to arbitrary shell command text — the same idea as a
bash alias (e.g. alias gs='git status'). It isn't required to be a
registered uc script. Invocation args are automatically appended at the
end, e.g. 'uc alias add gs git status' then 'uc gs -s' runs 'git status -s'.

Options go before the command:
'uc alias add gs --desc "short status" git status'. Everything from the
first command word on is the alias, including any flags. Use '--' before a
command that starts with a dash. --desc sets the description shown in
'uc list' and the picker.

'uc alias list' shows aliases only. 'uc alias edit' opens the command text
in $EDITOR.
`,
	"function": `usage: uc function add <name> <body> [--desc <text>]
   or: uc function remove <name>
   or: uc function list
   or: uc function edit <name>

A function is a named, inline shell snippet run via 'bash -c'. Useful for
chaining multiple 'uc' commands together, e.g.
'uc build && uc test && uc deploy'. The body is one argument, so quote it;
your args are available inside it as $1, $2, and $@.

'uc function list' shows functions only. 'uc function edit' opens the body
in $EDITOR. --desc sets the description shown in 'uc list' and the picker.
`,
	"sync": `usage: uc sync init <remote-url>
   or: uc sync push
   or: uc sync pull [--force]
   or: uc sync status

Sync scripts, aliases, and functions across machines through a git remote
you own (use a PRIVATE repo — scripts may contain secrets). uc shells out
to git; there is no hosted backend, and git must be on PATH.

The remote is cloned into $UC_HOME/sync, laid out as:

  scripts/        mirror of $UC_HOME/scripts
  commands.json   copy of $XDG_CONFIG_HOME/uc/commands.json

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
	"export": `usage: uc export [<file>] [--force]

Write every script, alias, and function to a gzip-compressed tar archive,
for moving to another machine, a backup, or sharing. The file defaults to
./uc-export-<hostname>-<YYYYMMDD>.tar.gz; '-' writes the archive to stdout:

  uc export - | ssh other-machine 'uc import -'

settings.json and history.json are never included. The archive is created
private to you (mode 600) and may contain secrets — treat it like a
private key. An existing file is not overwritten unless you pass --force.
`,
	"import": `usage: uc import <file> [--force] [--dry-run]

Add the scripts, aliases, and functions from an archive made by
'uc export'. '-' reads the archive from stdin.

Import merges: nothing on this machine is deleted, and entries identical
to what you already have are skipped. If an entry exists here with
different content, nothing at all is imported unless you pass --force, in
which case the archive's version wins. --dry-run lists what would change
and writes nothing.

An archive may come from someone else, so it is checked before anything is
written: only scripts/<name> and commands.json are accepted, only regular
files, every name must be valid, and size limits apply (1 MB per file,
1000 entries, 50 MB in total). Review imported scripts before running them.
`,
	"help": `usage: uc help [<command>|<name>]

With no argument, print the overall usage. With an argument, print help for
a uc command, or a script/alias/function's @desc/@usage/@example metadata.
'uc <name> --help' (sole argument) is a shortcut for 'uc help <name>'.
`,
	"version": `usage: uc version

Print the version string. 'uc -v' and 'uc --version' do the same.
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
