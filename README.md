# uc

`uc` is a single command that dispatches to your personal collection of
scripts. Register a script once, then run it from anywhere:

```
uc <name> [args...]
```

No `$PATH` editing, no per-script wrapper, no remembering where you put
that thing you wrote six months ago.

## Install

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
and place the `uc` binary on your `$PATH`.

### From source

```sh
make install
```

Builds a universal macOS binary (Intel + Apple Silicon, via `lipo`) and
copies it to `~/bin/uc`. Run `make build-mac` alone if you just want the
binary at `dist/uc` without installing it, or `make build` for a
single-arch build for your current platform.

Then add to your `~/.bashrc` or `~/.zshrc`:

```sh
export PATH="$HOME/bin:$PATH"
eval "$(uc completion bash)"   # or: uc completion zsh
```

## Quick start

```sh
$ uc add ~/scripts/killport.sh killport
added /Users/you/scripts/killport.sh -> /Users/you/.uc/scripts/killport

$ uc list
killport  kills a process by port

$ uc killport 8080
would kill port 8080
```

`uc` hands the script direct control of the terminal — same stdin/stdout,
same signal handling, same exit code, as if you'd run it yourself. It does
this via process replacement (`execve`), not by spawning a subprocess.

A name can resolve three ways, in this fixed order: a **script**, then an
**alias** (a name for arbitrary shell command text, the same idea as a bash
alias), then a **function** (an inline shell snippet). `uc which <name>`
always tells you which kind you got.

## Commands

| Command | What it does |
|---|---|
| `uc <name> [args...]` | Run a registered script, alias, or function |
| `uc` *(no args)* | Launch the interactive fuzzy picker |
| `uc list` / `uc ls` | List scripts + aliases + functions, with kind and description |
| `uc add <path> [name]` | Register a script (copies it in, sets +x) |
| `uc remove <name>` / `uc rm` | Unregister a script, alias, or function |
| `uc which <name>` | Print what a name resolves to, and its kind |
| `uc edit <name>` | Scripts: open in `$EDITOR`. Aliases/functions: temp-file round-trip through `$EDITOR`. |
| `uc completion bash\|zsh` | Print a shell completion script |
| `uc history [n]` | Show the last n invocations (default/max: `history_size`), with kind |
| `uc history run <n>` | Re-run the nth history entry |
| `uc alias add <name> <command...> [--desc "..."]` | Alias `<name>` to arbitrary shell command text |
| `uc alias remove <name>` | Delete an alias |
| `uc alias list` | List aliases only |
| `uc alias edit <name>` | Edit an alias's command via a temp-file round-trip through `$EDITOR` |
| `uc function add <name> <body> [--desc "..."]` | Register an inline shell snippet |
| `uc function remove <name>` | Delete a function |
| `uc function list` | List functions only |
| `uc function edit <name>` | Edit a function's body in `$EDITOR` |
| `uc help` | Show usage |
| `uc help <subcommand\|name>` | Help for a subcommand, or a script/alias/function's `@desc`/`@usage`/`@example` |
| `uc <name> --help` / `uc <name> -h` | Shortcut for `uc help <name>`, only when it's the sole argument |
| `uc version` | Print version |

Reserved subcommand names (including `alias`/`function` themselves) always
win over a script/alias/function of the same name, so `uc add`,
`uc alias add`, and `uc function add` all reject them up front.

## Writing scripts

Any language works, as long as it's either executable with its own shebang
or has one of these extensions: `.sh`, `.py`, `.js`, `.rb`, `.pl`. `uc`
looks up the right interpreter on `$PATH` at runtime.

Add metadata tags that show up in `uc list`, the picker, and `uc help
<name>`:

```sh
#!/bin/bash
# @desc: kills whatever is listening on a port
# @usage: killport <port>
# @example: killport 8080
# @example: killport 3000 --force
```

Each comment must start with `#` and appear in the first 25 lines.
`@desc`/`@usage` are one line each; `@example` is repeatable — add as many
as you want.

If two scripts share a bare name with different extensions (e.g.
`killport.sh` and `killport.py`), resolving that name is an error rather
than silently picking one — rename one of them.

## Aliases

An alias maps a name to arbitrary shell command text — the same idea as a
bash alias (`alias gs='git status'`), scoped to `uc`'s own registry. It
isn't required to be a registered script; anything runnable by `bash -c`
works:

```sh
$ uc alias add gs git status
added alias gs -> git status

$ uc gs -s
# runs: git status -s
# (your args are appended at the end, same as bash's own alias expansion)
```

Multiple words after `<name>` are joined with spaces, so quoting is only
needed for shell syntax you want preserved as one alias, e.g.
`uc alias add ll 'ls -la | less'`. To chain to another `uc` name, write `uc`
explicitly: `uc alias add dp uc deploy --env=production`.

`--desc "..."` sets the description shown in `uc list`, the picker, and
`uc help <name>` — same role as a script's `@desc` tag.

`uc alias edit <name>` (or plain `uc edit <name>`) opens the command text
in `$EDITOR` via a temp-file round-trip, like `uc function edit` — a
non-zero editor exit aborts the save. Aliases are stored in
`$XDG_CONFIG_HOME/uc/aliases.json`.

## Functions

A function is a named, inline shell snippet — the natural way to chain
`uc` commands together, since `uc` is already callable from within the
snippet's own body:

```sh
$ uc function add shiplt 'uc build && uc test && uc deploy --env=production'
added function shiplt

$ uc shiplt
# runs the chain via `bash -c`; $1/$2/$@ inside the body map to whatever
# args you pass to `uc shiplt`
```

`uc function edit <name>` opens the body in `$EDITOR` via a temp-file
round-trip (there's no file to open directly) — a non-zero editor exit
aborts the save. Functions always run via `bash -c`, regardless of your
`$EDITOR`/interpreter settings. They're stored in
`$XDG_CONFIG_HOME/uc/functions.json`.

## Interactive picker

Running `uc` with no arguments opens a fuzzy-filter list of your scripts,
aliases, and functions together — each tagged with its kind — ranked by
frecency (how often and how recently you've used each one):

- Type to filter by name or description.
- `↑`/`↓` or `Ctrl-p`/`Ctrl-n` to move the selection.
- `Enter` selects and drops you into an editable arg line, pre-filled with
  `<name> ` — most scripts take arguments, so you get a chance to add them
  before it runs.
- `Enter` again runs it; `Esc`/`Ctrl-c` exits without running anything.

## Tab completion

`uc completion bash|zsh` emits a thin shell function that calls back into
`uc __complete <partial>` on every `<TAB>`, so completions are ranked by
frecency, not just alphabetical.

## History & frecency

Every run (script, alias, or function — not management commands) is
logged to `$UC_HOME/history.json` — name, args, timestamp, and kind —
capped at `history_size` entries (25 by default). Because `uc` replaces
its own process on exec, it can only record what it was about to run, not
the exit code or duration. `uc history run <n>` re-resolves the name fresh
at replay time, so an edited alias/function definition is picked up rather
than replaying a stale snapshot.

Ranking (for the picker and tab completion) combines frequency and recency
within that history window, uniformly across all three kinds — frecency
is computed on `name` alone, regardless of what it resolves to:

| Last used | Weight |
|---|---|
| < 1 hour ago | 4× |
| < 1 day ago | 3× |
| < 1 week ago | 2× |
| older / not in the window | 1× |

## File layout

```
$XDG_CONFIG_HOME/uc/
├── settings.json                   # optional, user-authored config
├── aliases.json                    # your `uc alias add` entries
└── functions.json                  # your `uc function add` entries

~/.uc/                              # $UC_HOME, overridable via env var
├── scripts/                        # every registered script, flat
└── history.json                    # last history_size invocations (25 by default)
```

`$UC_HOME` defaults to `~/.uc`. `settings.json` is never created
automatically — it's only read if you put one there yourself:

```json
{
  "editor": null,
  "history_size": 25,
  "completion": { "enabled": true },
  "frecency": {
    "recency_weights": { "hour": 4, "day": 3, "week": 2, "older": 1 }
  }
}
```

Unknown keys are ignored, so old config files keep working across
upgrades. Unset keys fall back to the defaults above.

## Releasing

1. Make sure `CHANGELOG.md` is up to date.
2. Create and push a tag:
   ```sh
   git tag v0.1.0
   git push origin v0.1.0
   ```
3. Go to **Actions → release → Run workflow**, enter the tag, and dispatch it.
   GoReleaser will build cross-platform binaries, create a GitHub Release, and
   update the Homebrew tap at `github.com/eftakhairul/homebrew-uc`.

## Development

```sh
make build     # single-arch build for your current platform -> dist/uc
make build-mac # universal macOS binary -> dist/uc
make test
make vet
make fmt
make clean
```

`uc version` reports `git describe --tags` (e.g. `v0.2.0` or
`v0.2.0-3-gabc1234-dirty`), baked in at build time via `-ldflags`. Without
a git repo or tags, it falls back to `dev`. Tag a release with
`git tag v0.1.0` before building to get a real version string.

Module boundaries mirror the architecture doc (`uc-ARCHITECTURE.md`):
`registry` (three-tier name resolution + metadata tags), `aliases` +
`functions` (their respective JSON stores), `executor` (process
replacement, including `bash -c` for functions), `history` + `ranker`
(frecency, uniform across all three kinds), `completion` + `picker` (the
two autocomplete surfaces), `help` (static + metadata-driven help
rendering), and `commands` (thin CLI handlers), all wired together by
`cmd/uc`.
