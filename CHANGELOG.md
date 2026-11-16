# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `uc sync init|push|pull|status` syncs scripts, aliases, and functions
  across machines through a git remote you own (uc shells out to `git`; no
  hosted backend). The remote is cloned into `$UC_HOME/sync`. `init` adopts
  a remote that already has state, or pushes this machine's state to an
  empty one. Push and pull are mirrors, so deletions propagate too.
  `history.json` and `settings.json` are never synced. Pull refuses to
  discard unpushed local changes unless given `--force`, and replays a
  rejected push's commit on top of the remote's, so non-conflicting edits
  from two machines are both kept. `sync` is now a reserved subcommand
  name, so it wins over a script of the same name (minor breaking change).
- `uc add <url> [name] [--force]` registers a script straight from an
  `http://` or `https://` URL. The name defaults to the last URL path
  segment; a gist page URL (`gist.github.com/<user>/<id>`) fetches the gist's
  first file via `/raw` and needs an explicit name. The same name,
  overwrite, and ambiguity guards as a local add run before anything is
  downloaded. Downloads are capped at 1 MB, must return 200 with a non-empty
  body, and follow redirects. uc prints a reminder to review the script
  before its first run and never runs it on its own.
- `uc add` now installs scripts atomically (temp file + rename), for local
  paths as well as URLs. A failed copy or download leaves no partial file
  behind and never destroys the script that `--force` was replacing.

- `uc new <name> [--lang sh|py|js|rb|pl]` scaffolds a script: it writes a
  template (shebang plus an empty `@desc`/`@usage`/`@example` block) directly
  into `$UC_HOME/scripts/` and opens `$EDITOR` on it, so saving the file
  registers it — no separate `uc add` step. Quitting with the template
  unchanged, or a non-zero editor exit, registers nothing and leaves no file
  behind. Names already taken by a script, alias, or function are refused.
  `new` is now a reserved subcommand name, so it wins over a script of the
  same name (minor breaking change).

- `uc completion fish` emits a fish completion script. Fish autoloads it from
  `~/.config/fish/completions/uc.fish` (no `eval` at shell startup) and shows
  each candidate's description — a script's `@desc` tag or an alias/function's
  `--desc` — in the completion pager. Frecency order is preserved. Bash and
  zsh output is unchanged.
- `uc export [<file>] [--force]` and `uc import <file> [--force] [--dry-run]`
  move scripts, aliases, and functions between machines as a `.tar.gz`
  (`-` for stdout/stdin, e.g. `uc export - | ssh other 'uc import -'`).
  Export never includes `settings.json` or `history.json` and creates the
  file `0600`. Import merges (never deletes), skips identical entries, and
  imports nothing if any entry differs unless `--force`. Every archive is
  validated before anything is written: only `commands.json` and
  `scripts/<name>`, only regular files, valid names, and size limits (1 MB
  per file, 1000 entries, 50 MB total). `export` and `import` are now
  reserved names (minor breaking change).
- `-h` / `--help` and `-v` / `--version`, plus `uc <command> --help` as a
  shortcut for `uc help <command>`.
- `uc history clear` empties the recorded history.
- `uc add <url> --insecure` allows plain `http://` downloads (see Security).
- An unknown name now suggests the closest registered or built-in names,
  like git: `'kilport' is not a uc command or a registered name. The most
  similar name is killport`.

### Changed

- `uc help` is now a git-style overview: a usage synopsis, then one line per
  command grouped under headings, with details behind `uc help <command>`.
  Every command's help opens with a `usage:` / `or:` block.
- Bare `uc` with nothing registered prints the help overview instead of a
  one-line message; `uc list` on an empty registry says
  `Nothing registered yet — see 'uc help' to get started`.
- Usage mistakes are reported in one shape — what was wrong, the command's
  usage, and `See 'uc help <command>'` — and **exit with status 2** (was 1).
  Unknown options, missing values, and extra arguments are now errors
  instead of being silently ignored (e.g. `uc list junk`, `uc history 3
  junk`). Error messages no longer contain Go internals such as
  `strconv.Atoi: parsing "-n": invalid syntax`.
- `uc help <unknown>` is an error (exit 1) instead of printing "No help
  available" and exiting 0.
- `uc alias add`: uc's own options must come **before** the command
  (`uc alias add gs --desc "short status" git status`). Everything from the
  first command word on is the alias, so a command's own `--desc` flag is no
  longer taken by uc. A `--desc` placed after the command now becomes part
  of the alias, and uc prints a note showing where to move it. Use `--`
  before a command that starts with `-` (breaking change).
- `uc function add` takes the body as exactly one argument; extra words are
  an error asking you to quote the body instead of being dropped silently.
- Aliases and functions are stored together in
  `$XDG_CONFIG_HOME/uc/commands.json` (`{"version": 1, "aliases": {...},
  "functions": {...}}`, unset fields omitted). Existing `aliases.json` and
  `functions.json` are read as-is and merged on the first change; the old
  files are kept as `*.migrated`. A name can no longer be both an alias and
  a function. The `uc sync` repo layout changes to `scripts/` +
  `commands.json`; a repo in the old layout is converted automatically —
  upgrade every machine before syncing again.
- Names for scripts, aliases, and functions must use letters, digits, `.`,
  `_` and `-`, start with a letter, digit or `_`, and be at most 64
  characters. Existing entries with other names still run and can be
  removed.
- A broken or out-of-range `settings.json` is now a warning, and the
  defaults are used — it no longer stops every command (including
  `uc help`). `"history_size": 0` turns history off; the maximum is 10000.
- `uc add` refuses a name already used by an alias or function unless
  `--force`.
- The picker's argument line follows shell quoting (`commit -m "two
  words"` passes one argument), and the `editor` setting is split the same
  way, so a quoted path with spaces works.
- Without a terminal, `uc` explains that the picker needs one instead of
  printing a library error.
- New directories and scripts are created `0700` (was `0755`). Existing
  ones are not changed; tighten them with
  `chmod 700 ~/.uc ~/.uc/scripts ~/.config/uc`.

### Security

- Names containing `../` or path separators could resolve outside the
  scripts directory: `uc remove ../x` deleted, and `uc ../x` executed, files
  elsewhere on disk. Lookups now reject such names.
- `uc sync init` passed the remote URL to `git clone` where a value starting
  with `-` was read as an option (`--upload-pack=<command>` runs a command).
  Such URLs are rejected, and the URL is always passed after `--`.
- `uc add` refuses plain `http://` downloads (and `https` → `http`
  redirects) unless `--insecure`; loopback addresses are exempt. A response
  that is an HTML page is refused. Redirects are capped at 5.
- Control characters (ANSI escape sequences) in a script's `@desc`/`@usage`/
  `@example` or an alias/function's metadata are stripped before printing,
  so a downloaded script can't rewrite terminal output. `uc which` shows an
  alias or function body with control characters escaped.
- An empty alias command or function body is rejected; previously
  `uc alias add e ''` made `uc e <args>` run the arguments as a command.
- History records arguments (including any token typed on the command
  line); `uc history clear` and `"history_size": 0` give a way out.

### Fixed

- Pressing space in the picker inserted two spaces, so a filter containing
  a space never matched.
- `.DS_Store` and other dotfiles in the scripts directory were listed as
  scripts.
- `"history_size": 0` made `history.json` grow without bound while showing
  nothing.
- `uc sync` failed on a machine with no git identity configured; sync
  commits now fall back to `uc <uc@hostname>` and skip commit signing.
  Network steps time out instead of hanging on an unreachable remote, and
  git failures say what uc was doing instead of showing the internal git
  command line.
- A run that could not start (e.g. a missing interpreter) was still recorded
  in history and ranked.
- Concurrent uc processes could lose a history entry or an alias/function
  change; updates to `history.json` and `commands.json` are now locked.
- `uc function edit` kept the editor's trailing newline; saving an empty
  file from `uc alias edit` / `uc function edit` now changes nothing.
- An ambiguous name (`foo.sh` and `foo.py`) now says how to fix it:
  `uc remove foo.py`.
- A script with no shebang, or without its executable bit, now fails with
  advice instead of `exec format error` / `permission denied`.

- `.py` scripts now run where only `python` exists (Windows, minimal Linux
  images): interpreters are resolved from an ordered candidate list, trying
  `python3` first and falling back to `python`.
- `uc alias add`/`uc alias edit` warn when the command text ends in a
  control operator or contains a comment, which would silently swallow or
  detach the invocation args appended as `"$@"`.
- Scripts whose first 25 lines contain a line larger than the metadata
  scanner's buffer (minified files, binaries) no longer disappear from
  `uc list`, the picker, and completions — they list without a description
  instead of being skipped.
- `uc add` no longer writes outside the scripts directory: names containing
  path separators or `..` are rejected. It also refuses to silently
  overwrite an existing script or create a cross-extension ambiguity —
  pass `--force` to overwrite deliberately.
- Windows binaries now actually run commands: Go's Windows `syscall.Exec` is
  an unsupported stub (always `EWINDOWS`), so every `uc <name>` invocation
  failed at runtime. On Windows uc now spawns the command with inherited
  stdio and mirrors its exit code. Scripts, aliases, and functions require
  `bash` on `PATH` (Git Bash).
- Removed the `completion/*` glob from the GoReleaser archive file list — the
  directory does not exist (completion scripts are emitted by `uc completion`).

## [0.1.0] - TBD

### Added

- Initial public release of `uc`.
- Script, alias, and function registry with frecency-ranked picker and completions.
- GitHub Releases and Homebrew distribution via GoReleaser.
