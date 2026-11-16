# `uc` How-To Guide

`uc` is a single command that dispatches to your personal collection of scripts,
aliases, and shell functions. Register something once, then run it from anywhere.

## Table of Contents

1. [Installation](#installation)
2. [Scripts](#scripts)
3. [Aliases](#aliases)
4. [Functions](#functions)
5. [The Interactive Picker](#the-interactive-picker)
6. [Tab Completion](#tab-completion)
7. [History](#history)
8. [Listing and Inspecting](#listing-and-inspecting)
9. [Editing](#editing)
10. [Removing](#removing)
11. [Sync Across Machines](#sync-across-machines)
12. [Export and Import](#export-and-import)
13. [Getting Help and Reading Errors](#getting-help-and-reading-errors)
14. [Configuration](#configuration)

## Installation

### macOS / Linux via Homebrew

```sh
brew install eftakhairul/uc/uc
```

### Via Go

```sh
go install github.com/eftakhairul/uc/cmd/uc@latest
```

### Prebuilt binaries

Download the archive for your platform from the
[GitHub Releases](https://github.com/eftakhairul/uc/releases) page, extract it,
and place `uc` on your `$PATH`.

### Shell setup

Add completion to your shell:

```sh
# Bash
	eval "$(uc completion bash)"

# Zsh
	eval "$(uc completion zsh)"

# Fish (install once — fish autoloads it, no eval needed)
	uc completion fish > ~/.config/fish/completions/uc.fish
```

## Scripts

Register any executable script with a shebang or one of these extensions: `.sh`,
`.py`, `.js`, `.rb`, `.pl`. `uc` copies it into `~/.uc/scripts/` and runs it via
process replacement (`execve`), so stdin/stdout, signals, and exit codes work
exactly as if you ran it directly.

### Add a script

```sh
uc add ~/scripts/killport.sh killport
# added /Users/you/scripts/killport.sh -> /Users/you/.uc/scripts/killport
```

If the name is already registered (or would be ambiguous with a same-named
script under another extension), `uc add` refuses rather than silently
overwriting. Re-register a script deliberately with `--force`:

```sh
uc add ~/scripts/killport.sh killport --force
```

### Add a script from a URL

Pass an `https://` URL instead of a path and `uc` downloads the script (up
to 1 MB) and registers it:

```sh
uc add https://raw.githubusercontent.com/you/dotfiles/main/killport.sh
# added https://raw.githubusercontent.com/you/dotfiles/main/killport.sh -> /Users/you/.uc/scripts/killport.sh
# note: review it before first run — uc which killport
```

The name defaults to the last segment of the URL path (query strings and
fragments are ignored); pass one explicitly to override it. A gist page URL
fetches the gist's first file through `/raw`. It has no file name to derive
a name from, so name it yourself:

```sh
uc add https://gist.github.com/you/abc123 killport
```

A URL install follows the same rules as a local one: an existing or
ambiguous name, or one already used by an alias or function, is refused
unless you pass `--force`. Those checks run before anything is downloaded,
and nothing is written unless the download succeeds. Up to 5 redirects are
followed.

Plain `http://` is refused, because anyone on the network path could change
the script before it reaches you:

```sh
uc add http://example.com/tool.sh
# uc: refusing to download over plain http — use an https URL, or pass --insecure

uc add http://intranet.local/tool.sh --insecure   # if you accept the risk
```

The same applies to an `https://` URL that redirects to `http://`.
Addresses on your own machine (`localhost`, `127.0.0.1`) are always allowed.

`uc` never runs what it downloads on its own. Use raw file URLs, not
repository web pages — a URL that returns a web page is refused with
`this URL returned a web page, not a script — use the raw file URL`. Read the
script (`uc which <name>` prints its path) before its first run.

### Create a script from scratch

When there's no file yet, `uc new` skips the write-it-then-add-it dance: it
writes a template into `~/.uc/scripts/` first and opens `$EDITOR` on it, so
saving the file is what registers it.

```sh
uc new killport
# (editor opens on ~/.uc/scripts/killport.sh)
# created /Users/you/.uc/scripts/killport.sh — run it with: uc killport
```

The scaffold arrives pre-filled with a shebang and an empty metadata block,
ready to fill in:

```sh
#!/usr/bin/env bash
# @desc:
# @usage: killport <args>
# @example: killport

set -euo pipefail
```

Pick another language with `--lang` (`sh`, `py`, `js`, `rb`, `pl`; default
`sh`) — it sets both the shebang and the file extension:

```sh
uc new report --lang py   # ~/.uc/scripts/report.py, python3 shebang
```

Two ways nothing gets registered: quit the editor without touching the
template (like `git commit` with an unedited message) and `uc new` prints
`aborted, nothing registered`; exit the editor non-zero and it reports the
failure. Either way no file is left behind. `uc new` also refuses a name
that's already taken by a script, alias, or function, or that's a reserved
subcommand — use `uc edit <name>` to change something that exists.

### Run a script

```sh
uc killport 8080
```

### Helpful metadata tags

Add these comments in the first 25 lines of a script:

```sh
#!/bin/bash
# @desc: kills whatever is listening on a port
# @usage: killport <port>
# @example: killport 8080
# @example: killport 3000 --force
```

These populate `uc list`, the interactive picker, and `uc help killport`.

## Aliases

Aliases are shortcuts for arbitrary shell command text, like bash aliases but
scoped to `uc`.

### Add an alias

```sh
uc alias add gs git status
# added alias gs -> git status

uc gs -s
# runs: git status -s
```

Args you pass are appended at the end, just like bash alias expansion.

### Gotcha: trailing `#`, `&`, `;` and `|`

Because your args are appended to the command text as `"$@"`, a command that
ends in a comment or a control operator changes what that append means:

```sh
uc alias add x 'echo hi # my note'   # -> echo hi # my note "$@"   (args ignored)
uc alias add y 'long-task &'         # -> long-task & "$@"         (args run on their own)
```

`uc alias add`/`uc alias edit` print a note when they spot this. It's a
warning, not a rejection — if the composition is what you meant, nothing
changes.

### Alias with a pipeline

If the command contains shell characters, quote the whole thing:

```sh
uc alias add ll 'ls -la | less'
```

### Alias that calls another `uc` name

```sh
uc alias add dp 'uc deploy --env=production'
```

### Add a description

Options go **before** the command:

```sh
uc alias add gs --desc "short git status" git status
```

Everything from the first command word on is the alias — including any
flags, so a tool's own `--desc` is never taken by `uc`:

```sh
uc alias add mk mytool create --desc "new item" --verbose
# alias runs: mytool create --desc new item --verbose
```

If you put `--desc` after the command out of habit, the alias is still
added, and `uc` shows where to move it:

```sh
uc alias add gs git status --desc "short git status"
# added alias gs -> git status --desc short git status
# uc: note: '--desc' is part of the alias command. To set a description, put it before the command:
#           uc alias add gs --desc "short git status" git status
```

For a command that itself starts with `-`, put `--` in front of it:
`uc alias add x -- --weird-binary -v`.

## Functions

Functions are inline shell snippets that chain commands together.

### Add a function

```sh
uc function add shiplt 'uc build && uc test && uc deploy --env=production'
# added function shiplt
```

Run it like any other name:

```sh
uc shiplt
```

Function bodies run via `bash -c`, and `$1`, `$2`, `$@` inside the body map to
whatever args you pass to `uc shiplt`.

### Function with arguments

```sh
uc function add greet 'echo "Hello, $1"'
uc greet Alice
# Hello, Alice
```

## The Interactive Picker

Run `uc` with no arguments to open a fuzzy list of all registered names:

```sh
uc
```

- Type to filter by name or description.
- Use `↑`/`↓` or `Ctrl-p`/`Ctrl-n` to move.
- Press `Enter` to select; you get an editable arg line pre-filled with the name.
- Press `Enter` again to run, or `Esc`/`Ctrl-c` to cancel.

Names are ranked by **frecency** — a combination of how often and how recently
they were used.

## Tab Completion

`uc completion bash|zsh|fish` emits a completion script. After loading it, tab
completion lists your registered names, ranked by frecency rather than
alphabetically.

```sh
uc kil<TAB>
# killport
```

### Fish

Bash and zsh load their script by `eval`ing it from your shell rc file on every
startup. Fish instead autoloads completions on demand from a file named after
the command, so you install it once and pay nothing at startup:

```sh
uc completion fish > ~/.config/fish/completions/uc.fish
```

The fish script also shows descriptions in the completion pager, which bash and
zsh do not:

```
uc <TAB>
# killport  (kills a process by port)
# gs        (git status)
```

Descriptions come from a script's `# @desc:` tag or an alias/function's
`--desc`. Entries without one complete as a bare name, and built-in
subcommands (`list`, `add`, ...) show no description — their help text is
multi-line prose. Internally, fish calls the hidden
`uc __complete --describe <partial>`, which emits `name<TAB>description`
lines; bash and zsh keep using the flag-less form.

## History

Every script, alias, and function run is logged.

### Show history

```sh
uc history       # last 25 entries
uc history 10    # last 10 entries
```

### Replay a history entry

```sh
uc history
# 1  killport 8080

uc history run 1
# runs killport 8080
```

`uc history run` re-resolves the name at replay time, so edits to aliases or
functions are picked up automatically.

### Clear or turn off history

History records the arguments you pass — including any token typed on the
command line. To wipe it:

```sh
uc history clear
# history cleared
```

To stop recording altogether, set `"history_size": 0` in `settings.json`;
`uc history` then says `history is turned off (history_size is 0)`.

## Listing and Inspecting

### List everything

```sh
uc list
# killport  script  kills whatever is listening on a port
# gs        alias   short git status
# shiplt    function  build, test, deploy
```

`uc ls` is a shorter alias for `uc list`.

### Find what a name resolves to

```sh
uc which killport
# script: /Users/you/.uc/scripts/killport
```

### Show help for a name

```sh
uc help killport
```

Or use the shortcut when `--help` is the only argument:

```sh
uc killport --help
```

See [Getting Help and Reading Errors](#getting-help-and-reading-errors) for
help on `uc`'s own commands.

## Editing

### Edit a script

```sh
uc edit killport
```

Opens the script in `$EDITOR`.

### Edit an alias or function

```sh
uc edit gs
uc edit shiplt
```

`uc` writes the current value to a temp file, opens it in `$EDITOR`, and saves
it back. A non-zero editor exit aborts the change.

## Removing

### Remove a script, alias, or function

```sh
uc remove killport
uc rm gs
uc rm shiplt
```

## Sync Across Machines

`uc sync` syncs your scripts, aliases, and functions between machines
through a git remote you own. `uc` just runs `git` — there is no hosted
service — so `git` must be on your `PATH`, and its usual authentication
(SSH keys, credential helpers) applies.

> Use a **private** repository. Scripts and alias commands can contain
> tokens and hostnames you don't want public.

### Set it up

Create an empty private repo, then on the machine that has your scripts:

```sh
uc sync init git@github.com:you/uc-sync.git
# + scripts/killport
# + aliases.json
# pushed initial state to git@github.com:you/uc-sync.git
```

On every other machine, run the same command. Because the remote already
has state, it is pulled in instead:

```sh
uc sync init git@github.com:you/uc-sync.git
# pulled existing state from git@github.com:you/uc-sync.git
uc killport 8080   # works right away
```

If that machine already has its own, different scripts, `init` stops
rather than overwrite them. Run `uc sync pull --force` to replace them with
the remote's, or `uc sync push` to replace the remote's with them.

### Push and pull

```sh
uc sync push     # make the remote match this machine
uc sync pull     # make this machine match the remote
```

Both are mirrors: a script you add, edit, or **remove** on one machine is
added, edited, or removed on the other. The output lists each file:
`+` added, `~` changed, `-` deleted.

What syncs, and what doesn't:

| Synced | Not synced |
|---|---|
| `$UC_HOME/scripts/` | `history.json` (per-machine usage) |
| `commands.json` (aliases and functions) | `settings.json` (per-machine, e.g. `editor`) |
| | dotfiles and subdirectories inside `scripts/` |

### Check where you are

```sh
uc sync status
# remote: git@github.com:you/uc-sync.git
# local changes not yet pushed:
#   + scripts/deploy
# 0 commit(s) to push, 1 to pull
```

`status` works offline too — it warns and compares against the last
fetched state.

### When two machines change things

- **Pull refuses to lose work.** If this machine has changes you haven't
  pushed, `uc sync pull` lists them and stops. Push them first, or run
  `uc sync pull --force` to discard them.
- **Push after someone else pushed.** `uc sync push` fails with
  `remote has newer changes — run uc sync pull first`. Your changes are
  already committed locally, so `uc sync pull` replays them on top of the
  remote's, then `uc sync push` publishes the result. Edits to *different*
  files from both machines are all kept.
- **Same file edited on both.** Pull stops with `sync repo has diverged`.
  Resolve it with git in `$UC_HOME/sync` (`git pull --rebase`, fix the
  file, `git add` it, `git rebase --continue`), then run
  `uc sync pull --force` to apply the result and `uc sync push` to
  publish it.

### How it's stored

The remote is cloned into `$UC_HOME/sync`, laid out as `scripts/` and
`commands.json`. Your live files stay where they always are; `uc sync`
copies between them and that clone. The remote URL lives in the clone's own
git config — there is no `settings.json` key.

A repo created by an older `uc` holds `aliases.json` and `functions.json`
instead. The first sync from this version converts it to `commands.json`
in a commit of its own and publishes it; upgrade `uc` on your other
machines before they sync again.

Sync commits don't depend on your git setup: on a machine with no git
identity, `uc` commits as `uc <uc@hostname>`, and commit signing is turned
off for these commits so nothing prompts. A remote that can't be reached
times out with `could not reach the remote (timed out)` instead of hanging.

## Export and Import

`uc sync` keeps machines in step continuously. For a one-off move, a
backup, or handing scripts to someone, write an archive instead:

```sh
uc export
# exported 12 scripts, 5 aliases, 2 functions to uc-export-laptop-20261116.tar.gz
# uc: note: the archive may contain secrets — treat it like a private key

uc export backup.tar.gz          # choose the file; --force to overwrite
uc export - | ssh desktop 'uc import -'
```

The archive contains `scripts/` and `commands.json`, never `settings.json`
or `history.json`, and is created readable only by you.

On the other machine:

```sh
uc import backup.tar.gz --dry-run
# + script killport
# ~ alias gs
# 1 to add, 1 to replace — nothing written (--dry-run)

uc import backup.tar.gz
# uc: 1 entry already exists with different content:
#   ~ alias gs
# pass --force to replace them, or --dry-run to preview

uc import backup.tar.gz --force
```

Import merges: it never deletes anything, skips entries that are already
identical, and imports nothing at all if any entry would replace something
different — unless you pass `--force`.

An archive may come from someone else, so `uc import` checks all of it
before writing anything, and rejects the whole archive if any of these
fail:

- only `commands.json` and `scripts/<name>` entries, no `..`, absolute
  paths, or nested folders;
- only regular files — no symlinks, hard links, or devices;
- every name valid (see below), and no empty alias or function;
- at most 1 MB per file, 1000 entries, and 50 MB in total.

Imported scripts get `uc`'s own file mode, never the one in the archive.

## Getting Help and Reading Errors

`uc help` (also `uc -h` or `uc --help`) prints a one-screen overview: one
line per command, grouped by what you use them for. With nothing registered
yet, bare `uc` prints the same overview.

```sh
uc help alias        # full usage and details for one command
uc alias --help      # same thing
uc -v                # version (also --version)
```

When a command is called the wrong way, `uc` says what was wrong, shows that
command's usage, and exits with status 2:

```sh
uc history -n
# uc: 'history' expects a number, got '-n'
# usage: uc history [<n>]
#    or: uc history run <n>
#    or: uc history clear
# See 'uc help history'.
```

An unknown name exits with status 127 and suggests close matches;
`uc help <unknown>` gives the same message but exits 1, since nothing was
meant to run:

```sh
uc kilport
# uc: 'kilport' is not a uc command or a registered name.
#
# The most similar name is
#         killport
```

Names for scripts, aliases, and functions use letters, digits, `.`, `_` and
`-`, start with a letter, digit or `_`, and are at most 64 characters. A
name can't be both an alias and a function.

## Configuration

`uc` works without any configuration file. If you want to customize behavior,
create:

```
$XDG_CONFIG_HOME/uc/settings.json
```

Example:

```json
{
  "editor": "vim",
  "history_size": 50,
  "completion": { "enabled": true },
  "frecency": {
    "recency_weights": { "hour": 4, "day": 3, "week": 2, "older": 1 }
  }
}
```

Unknown keys are ignored so older config files keep working across upgrades.

If the file can't be used — invalid JSON, a value of the wrong type, or a
value out of range (`history_size` must be 0–10000; weights can't be
negative) — `uc` prints a warning such as
`uc: warning: ignoring settings.json: "history_size" must be a whole number — using defaults`
and carries on with the defaults. A typo here never locks you out.

`editor` follows shell quoting, so a path with spaces works:
`"editor": "\"/Applications/My Editor\" --wait"`.

### Files

Aliases and functions live together in `$XDG_CONFIG_HOME/uc/commands.json`.
If you are upgrading from a version that used `aliases.json` and
`functions.json`, `uc` keeps reading those until your first change, then
merges them into `commands.json` and keeps the old files as `*.migrated`.

New directories and scripts are created readable only by you (mode `700`).
To tighten an existing install:
`chmod 700 ~/.uc ~/.uc/scripts ~/.config/uc`.
