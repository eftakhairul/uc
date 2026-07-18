# `uc` Technical Documentation

## Overview

`uc` is a personal command dispatcher written in Go. It lets you register scripts,
aliases, and shell functions, then invoke them by name from anywhere. The project
is published as a standalone CLI binary and distributed via GitHub Releases,
Homebrew, and `go install`.

## Repository Layout

```
.
├── cmd/uc                  # Application entry point
├── internal/
│   ├── aliases             # JSON-backed alias store
│   ├── commands            # CLI subcommand handlers
│   ├── completion          # Shell completion engine
│   ├── executor            # Process replacement (execve / bash -c)
│   ├── functions           # JSON-backed function store
│   ├── history             # Invocation history store
│   ├── picker              # Interactive fuzzy picker
│   ├── ranker              # Frecency ranking
│   ├── registry            # Three-tier name resolution
│   └── help                # Static and metadata-driven help
├── e2e                     # Containerized end-to-end tests
├── .github/workflows/      # CI/CD workflows
├── .goreleaser.yml         # Release configuration
├── Makefile                # Local development tasks
├── README.md               # User-facing documentation
├── CHANGELOG.md            # Release notes
└── docs/doc.md             # This document
```

## Module

```
github.com/eftakhairul/uc
```

Built with Go 1.26.4. The module path matches the GitHub repository, so `go install`
works out of the box.

## Core Concepts

### Three-tier name resolution

When you run `uc <name>`, the name resolves in this fixed order:

1. **Script** — executable file in `~/.uc/scripts/`
2. **Alias** — command text stored in `$XDG_CONFIG_HOME/uc/aliases.json`
3. **Function** — inline shell snippet stored in `$XDG_CONFIG_HOME/uc/functions.json`

`uc which <name>` reports the resolved kind and source.

### Execution model

Scripts are run with `execve` process replacement, preserving stdin/stdout,
signal handling, and exit codes. Aliases and functions run through `bash -c`.

### Metadata tags

Scripts can include structured comments in the first 25 lines:

```sh
#!/bin/bash
# @desc: short description
# @usage: mycmd <arg>
# @example: mycmd foo
# @example: mycmd bar
```

These tags feed `uc list`, the picker, and `uc help <name>`.

### Frecency

The picker and completions rank names by a frequency/recency score. Weights:

| Last used | Multiplier |
|-----------|------------|
| < 1 hour  | 4×         |
| < 1 day   | 3×         |
| < 1 week  | 2×         |
| older     | 1×         |

## Development

Local build:

```sh
make build      # current platform -> dist/uc
make build-mac  # universal macOS binary
make test
make vet
make fmt
make clean
```

Version is baked in at build time with `-ldflags` and falls back to `dev` when
no git tag is present.

## Release Process

Releases are fully automated via GoReleaser.

### Trigger a release

1. Update `CHANGELOG.md`.
2. Create and push a tag:
   ```sh
   git tag v0.1.0
   git push origin v0.1.0
   ```
3. Go to **Actions → release → Run workflow**, enter the tag, and dispatch.

### What GoReleaser does

- Builds cross-platform binaries:
  - macOS: `amd64`, `arm64`
  - Linux: `amd64`, `arm64`
  - Windows: `amd64`
- Produces `tar.gz` archives (`.zip` for Windows)
- Generates a checksum file
- Creates a GitHub Release with a categorized changelog
- Pushes an updated Homebrew formula to `github.com/eftakhairul/homebrew-uc`

### Required secrets

| Secret | Used for |
|--------|----------|
| `GITHUB_TOKEN` | Created automatically by GitHub Actions; creates release and uploads assets |
| `HOMEBREW_TAP_TOKEN` | Personal access token with `repo` and `workflow` scopes; updates the Homebrew tap |

## Distribution Channels

| Channel | Install command |
|---------|-----------------|
| Homebrew | `brew install eftakhairul/uc/uc` |
| Go | `go install github.com/eftakhairul/uc/cmd/uc@latest` |
| GitHub Releases | Download archive from the releases page |
| Source | `make install` |

## Configuration

User configuration is optional. When present, it lives at:

```
$XDG_CONFIG_HOME/uc/settings.json
```

Default structure:

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

Unknown keys are ignored for forward compatibility.

## Data Layout

```
$XDG_CONFIG_HOME/uc/
├── settings.json    # optional user config
├── aliases.json     # alias registry
└── functions.json   # function registry

$UC_HOME/           # defaults to ~/.uc
├── scripts/        # registered scripts
└── history.json    # last N invocations
```

## Testing

- Unit tests: `make test`
- End-to-end tests: `make test-e2e` (requires Docker)

## CI/CD

- `.github/workflows/release.yml`: `workflow_dispatch`-triggered release pipeline
- `.goreleaser.yml`: cross-platform build, archive, release, and tap update

## License

MIT. See `LICENSE`.
