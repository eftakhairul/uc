# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

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
