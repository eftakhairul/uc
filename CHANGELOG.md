# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
