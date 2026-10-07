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

### Fixed

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
