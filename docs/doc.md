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
11. [Configuration](#configuration)

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

```sh
uc alias add gs git status --desc "short git status"
```

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

`uc completion bash|zsh` emits a completion script. After loading it, tab
completion lists your registered names, ranked by frecency rather than
alphabetically.

```sh
uc kil<TAB>
# killport
```

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
