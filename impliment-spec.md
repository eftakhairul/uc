# uc — Implementation Specification

Detailed, feature-by-feature implementation spec for the next release. Features
are ordered small → large so value ships early and each lands as an
independently green `feat:` commit.

1. [Fish shell completion](#1-fish-shell-completion) — **shipped**
2. [`uc new` — scaffold a script](#2-uc-new--scaffold-a-script) — **shipped**
3. [`uc add <url>` — register from a URL/gist](#3-uc-add-url--register-from-a-urlgist) ← **next**
4. [`uc stats` + `uc history search`](#4-uc-stats--uc-history-search)
5. [Tags](#5-tags)
6. [Env injection](#6-env-injection)
7. [`uc sync` — git-backed sync](#7-uc-sync--git-backed-sync-no-server)

**Previously out of scope, now fixed — no bug work is pending here.** Both
items (the Windows `syscall.Exec` stub that made every `uc <name>` fail at
runtime, and the `.goreleaser.yml` `archives.files` `completion/*` glob that
matched nothing) landed ahead of this spec's feature work; see
`CHANGELOG.md` → `[Unreleased]` → `Fixed`. Windows now spawns the command
with inherited stdio (`internal/executor/exec_windows.go`).

---

## Cross-cutting checklist

Every new subcommand (`new`, `sync`, `stats`) and new verb/flag
(`history search`, `list --tag`, `__complete --describe`) touches the same
seams. Apply this checklist per feature:

| Seam | Change |
|---|---|
| `internal/reserved/reserved.go` | Append `"new"`, `"sync"`, `"stats"` to `Names` (reserved names always win over same-named scripts — note in CHANGELOG as minor breaking). |
| `cmd/uc/main.go` | New `case` in the flat dispatch switch + a hand-rolled `runX(r *commands.Runner, args []string) error` arg parser. Follow `runAlias` (main.go:169) for verb families and `runAliasAdd` (main.go:193) for flag scanning. **No flag package** — the repo hand-rolls all parsing. |
| `internal/commands/commands.go` | Handler method on `Runner`, normal output via `fmt.Fprintf(r.Out, ...)` (injectable for tests), warnings via `os.Stderr`. Update the `helpText` const (commands.go:620). |
| `internal/help/help.go` | `staticText` map entry (duplicate the entry verbatim for any alias name, matching the existing `list`/`ls` pattern). |
| Docs | `README.md` command table (starts README.md:88) and config section; `docs/doc.md` TOC + section; `CHANGELOG.md` under `## [Unreleased]` → `### Added` (the section already exists — the fish entry is there). |
| Commit | `feat: ...` prefix (goreleaser changelog grouping). |

**Existing utilities to reuse — do not reinvent:**

- `Cfg.EditorCommand()` (config.go:138) — settings.json `editor` → `$EDITOR` → `vi`; split with `strings.Fields`.
- `Runner.editViaTempFile(pattern, initial)` (commands.go:563) — child-process editor round-trip pattern.
- `commands.validScriptName(name)` (commands.go:110) — rejects `""`, `.`, `..`, path
  separators, and anything where `name != filepath.Base(name)`. Already called by
  `Add` (commands.go:130); reuse it for every new name-taking command.
- `registry.BareName` (registry.go:254), `registry.ExtractMeta` (registry.go:274), `Registry.List()` (registry.go:172).
- `reserved.Is` (reserved.go:31).
- `atomicfile.Write` (atomicfile.go:14) — JSON stores only; it does temp+rename but no exec bit, so never use it for scripts without a follow-up chmod.
- `executor.ExecReplace` (executor.go:113) — only for flows that never need control back.
- E2E helpers: `sandbox`, `runUC`, `writeFile`, `withEnv`, `fake-editor` (`$EDITOR_NEW_CONTENT`), `fake-editor-fail` (e2e/e2e_test.go, e2e/cli_test.go).

**Test conventions:** unit tests are stdlib-only (no testify), `t.TempDir()` +
`t.Setenv`, error types checked via `errors.AsType[*T]`. E2E tests
(`//go:build e2e`, `make test-e2e`) use testify + testcontainers on
`debian:bookworm-slim`. `internal/commands/commands_test.go` **already
exists** (added by the `uc add` hardening and fish-completion work): use its
`newTestRunner(t)` helper (commands_test.go:18), which returns a `*Runner`
wired to a temp `UC_HOME` plus the `*bytes.Buffer` behind `Runner.Out`, and
its `writeScript(t, dir, name, content)` helper (commands_test.go:32). Every
feature below extends that file rather than creating it.

---

## 1. Fish shell completion

> **Status: shipped** — commit `7c51c01` (PR #8). Kept as the record of what
> landed. Where it lives now: `completion.Pair` (completion.go:19),
> `DescribedCandidates` (completion.go:43), `fishScript` (completion.go:128),
> `Runner.Complete(partial, describe)` (commands.go:266), `runComplete`
> (main.go:133), tests in `internal/completion/completion_test.go` and
> `TestCompleteDescribe*` in `commands_test.go`, docs at README.md:57 /
> README.md:208 and docs/doc.md:203. CHANGELOG entry written. The line
> references inside this section are as-designed, not as-built — read the
> files for current positions.

### Goal

`uc completion fish` emits a fish completion script. Fish gets two upgrades
over bash/zsh: per-candidate **descriptions** in the completion pager (from
`@desc` / alias / function descriptions) and lazy autoloading (no `eval` at
shell startup).

### CLI surface

```sh
uc completion fish > ~/.config/fish/completions/uc.fish   # recommended install
uc __complete --describe <prefix>                          # hidden, fish-only callback
```

### Design

The emitted script (one logical line):

```fish
# uc fish completion
complete -c uc -f -n "test (count (commandline -opc)) -eq 1" \
    -k -a "(uc __complete --describe (commandline -ct))"
```

- `-f` suppresses default filename completion.
- `-n "test (count (commandline -opc)) -eq 1"` completes only the **first**
  argument — the same guard as bash's `COMP_CWORD -eq 1` / zsh's `CURRENT -eq 2`;
  args after `uc <name>` belong to the script.
- `-k` preserves emission order — fish otherwise sorts alphabetically, which
  would silently destroy the frecency ranking.
- Candidate lines are `name<TAB>description` (bare `name` when no description);
  fish natively renders the part after the tab as the pager description.

`--describe` is a new optional first argument to the hidden `__complete`
subcommand. The bash/zsh scripts keep calling the flag-less form and their
output is byte-for-byte unchanged.

### File changes

**`internal/completion/completion.go`**

- New exported type and function; `Candidates` becomes a thin wrapper so the
  ranking logic exists once:

```go
type Pair struct {
    Name string
    Desc string
}

// DescribedCandidates: Candidates plus each candidate's description, for
// shells whose completion menu renders one (fish).
func DescribedCandidates(reg *registry.Registry, entries []history.Entry,
    weights ranker.Weights, prefix string) ([]Pair, error)

// Candidates(...) ([]string, error)  — unchanged signature, delegates to
// DescribedCandidates and strips descriptions.
```

- Implementation of `DescribedCandidates` mirrors today's `Candidates` body
  (completion.go:21-51) with one addition: while collecting `reg.List()`
  items, also record `descs[it.Name] = it.Desc`. Reserved subcommand names are
  appended with no description (v1 decision — `help.staticText` is multi-line
  prose, not usable as a one-liner).
- `Script()` (completion.go:56): add `case "fish": return fishScript, nil`;
  change the error to `unsupported shell %q (want bash, zsh or fish)`.
- New `const fishScript` (content above).
- Dedup note: a script named identically to a reserved name currently yields a
  duplicate candidate in bash/zsh too — existing behavior, leave as is
  (surgical-change rule).

**`internal/commands/commands.go`**

- `Complete` gains a parameter: `func (r *Runner) Complete(partial string, describe bool) error`.
  When `describe` is set, call `completion.DescribedCandidates` and print
  `fmt.Fprintf(r.Out, "%s\t%s\n", p.Name, p.Desc)` for pairs with a
  description, `fmt.Fprintln(r.Out, p.Name)` otherwise. The
  `CompletionEnabled()` early-return is unchanged.
- `helpText` const: `uc completion <shell>   Print a completion script (bash|zsh|fish)`.

**`cmd/uc/main.go`**

- `runComplete` (main.go:124): strip an optional leading `--describe`:

```go
func runComplete(r *commands.Runner, args []string) error {
    describe := false
    if len(args) >= 1 && args[0] == "--describe" {
        describe = true
        args = args[1:]
    }
    partial := ""
    if len(args) >= 1 {
        partial = args[0]
    }
    return r.Complete(partial, describe)
}
```

- `runCompletion` usage string (main.go:119): `usage: uc completion <bash|zsh|fish>`.

**`internal/help/help.go`** — `"completion"` entry: mention fish and the
autoload install path (`uc completion fish > ~/.config/fish/completions/uc.fish`).

**Docs** — README shell-setup block + command table; docs/doc.md
“Tab Completion” section (doc.md:167) gets a fish subsection explaining
autoloading vs `eval`.

### Edge cases

- `uc __complete --describe` with no prefix → all candidates, described.
- A literal prefix `--describe` cannot be completed — acceptable: `__complete`
  is hidden and shells always pass the token after the flag.
- Descriptions containing tabs/newlines: `@desc` values are single-line by
  construction (regex-captured from one line); JSON descs could contain a tab —
  harmless in fish (first tab wins), not worth sanitizing.

### Tests

New `internal/completion/completion_test.go` (stdlib style, model on
`registry_test.go` helpers):

- `Script("fish")` returns a script containing `complete -c uc`, `-k`, and
  `__complete --describe`; `Script("nope")` errors mentioning fish.
- `DescribedCandidates`: temp scripts dir with a `@desc`-tagged script + an
  alias with a desc + one without → pairs carry the right descs; prefix
  filtering; frecency order (seed history entries with distinct timestamps).
- In `commands_test.go` (once it exists, feature 2): `Complete("k", true)`
  writes `killport\tkills a port` to the buffer; `Complete("k", false)` writes
  the bare name.

### Docs/verify

`gofmt -l .` clean, `make vet && make test`. Manual: `dist/uc completion fish | fish -c 'source -'`
(syntax check) if fish is installed locally; otherwise the e2e container check
is deferred (bookworm-slim has no fish — do **not** add fish to the container
just for this; the unit tests cover the contract).

---

## 2. `uc new` — scaffold a script

> **Status: shipped** on `feature/add-script-editor`. Built as specced, with
> two deliberate deviations: (a) `templates` is an ordered **slice** of
> `scriptTemplate` rather than a map, so `--lang` error text lists the values
> in usage order; (b) `uc new killport.sh` reuses the given extension instead
> of producing `killport.sh.sh`. Only `sh` carries a `set -euo pipefail`
> preamble — the other four are shebang + metadata block, since a preamble
> would be language-specific boilerplate nobody asked for.

### Goal

Collapse “write a script somewhere, chmod it, `uc add` it” into one step:
`uc new killport` opens `$EDITOR` on a pre-filled template already inside
`$UC_HOME/scripts/`, so saving **is** registering.

### CLI surface

```sh
uc new <name> [--lang sh|py|js|rb|pl]     # default: sh
```

### Design

1. **Validate** `name`:
   - `validScriptName(name)` (commands.go:110) → `"invalid script name %q"`.
     Call it first, exactly as `Add` does (commands.go:130) — it already
     covers the `/`, `\`, `..` and leading-`.` cases below.
   - `reserved.Is(registry.BareName(name))` → `"%q is a reserved subcommand name"`
     (same message and same `BareName` wrapping as `Add`, commands.go:137).
   - `r.Reg.Resolve(name)` succeeds → `"%q already exists (%s) — use `uc edit %s`"`
     with the resolved kind. (`*NotFoundError` is the good path;
     `*AmbiguousNameError` also blocks.)
   - `--lang` not in the template map → error listing valid values.
2. **Create**: `Cfg.EnsureDirs()`, then write the language template to
   `filepath.Join(Cfg.ScriptsDir, name+ext)` with mode `0o755`
   (`os.WriteFile`). Extension per lang (`.sh`, `.py`, `.js`, `.rb`, `.pl`) —
   matches `extPriority` (registry.go:22) so `BareName` strips it at resolve
   time and the executor picks the right interpreter (the `interpreters` map,
   executor.go:21, which now holds an ordered candidate list per extension).
3. **Edit**: run the editor as a **child process** on the final path — the
   same `exec.Command` + wired `os.Stdin/Stdout/Stderr` pattern as
   `editViaTempFile` (commands.go:578-586), *not* `ExecReplace`, because uc
   must regain control to validate. Factor the “run editor on path, wait”
   portion into a small shared helper (e.g. `runEditor(path string) error`)
   used by both `editViaTempFile` and `New` rather than duplicating.
4. **Post-edit**:
   - Editor exited non-zero → `os.Remove` the file, error
     `"editor exited with an error, nothing registered"` (mirrors
     editViaTempFile's message shape).
   - File content byte-identical to the template (`os.ReadFile` + compare) →
     remove, print `aborted, nothing registered` to `r.Out`, return nil
     (mirrors `git commit` with an untouched message).
   - Otherwise print `created <dest> — run it with: uc <name>`.

Templates (map in `commands.go`, `lang → struct{ext, content string}`; the
content embeds the name):

```sh
#!/usr/bin/env bash
# @desc:
# @usage: <name> <args>
# @example: <name>

set -euo pipefail

```

Python/JS/Ruby/Perl variants differ only in shebang and comment leader — all
five use `#` comments, so the `@desc` block is identical and `ExtractMeta`
picks it up for every lang.

### File changes

- `internal/reserved/reserved.go` — `"new"` in `Names`.
- `cmd/uc/main.go` — `case "new": cmdErr = runNew(r, rest)`; `runNew` scans
  args for `--lang <v>` (the `--desc` scan pattern, main.go:187-194) and
  requires exactly one positional name:
  `usage: uc new <name> [--lang sh|py|js|rb|pl]`.
- `internal/commands/commands.go` — `func (r *Runner) New(name, lang string) error`
  + template map + `runEditor` helper extraction.
- `internal/help/help.go` — `"new"` entry.
- `helpText` const, README table + Quick start mention, docs/doc.md new
  “Creating a script” subsection under Scripts, CHANGELOG.

### Edge cases

- Name containing `/`, `\` or equal to `.`/`..` → rejected by the shared
  `validScriptName` (commands.go:110); it would escape or hide in
  `ScriptsDir`. Do **not** write a second validator — `Add` already uses this
  one, and its message (`invalid script name %q`) is the one to keep.
- A same-named file with a *different* extension already exists → the Resolve
  pre-check catches it (any extension resolves).
- `$EDITOR` unset → `EditorCommand()` falls back to `vi`; non-interactive
  environments (tests) set `EDITOR` to a stub.

### Tests

Added to the existing `internal/commands/commands_test.go` via
`newTestRunner(t)` (commands_test.go:18), with `EDITOR` pointed at a shell
stub script created in `t.TempDir()`:

- Happy path: stub editor appends a line → file exists in scripts dir, mode
  has 0o100 exec bit, output contains `created`; `Reg.Resolve(name)` finds it.
- Abort path: stub editor touches nothing → file removed, output `aborted`.
- Editor failure: stub exits 1 → file removed, error returned.
- Reserved name and existing-name rejections.
- `--lang py` → `.py` file with python shebang.

E2E (`e2e/cli_test.go`): `uc new t1` with `fake-editor` +
`EDITOR_NEW_CONTENT` containing a full script body → `uc t1` runs it;
abort path via `EDITOR_NEW_CONTENT` equal to the template is fiddly — cover
abort in unit tests only, keep e2e to the happy path.

---

## 3. `uc add <url>` — register from a URL/gist

### Goal

`uc add https://... [name]` downloads a script and registers it — the seed of
script sharing between people.

### CLI surface

```sh
uc add https://raw.githubusercontent.com/u/r/main/killport.sh
uc add https://gist.github.com/user/abc123 killport      # gist page → /raw
```

### Design

Branch at the top of `Runner.Add` (commands.go:119 — signature is now
`Add(srcPath, name string, force bool)`): if `srcPath` has prefix `http://`
or `https://`, take the fetch path; the local-file path is untouched. The
URL path must thread `force` through to the same overwrite/ambiguity
pre-checks the local path runs (commands.go:146-162) — a URL install is not
a licence to clobber. `runAdd` (main.go:85) already parses `--force`, so no
new flag scanning is needed.

1. **URL rewrite** — pure function `rawURL(raw string) (string, error)`:
   parse with `net/url`; if host is `gist.github.com` and the path does not
   already end in `/raw` or contain `/raw/`, append `/raw` (fetches the
   gist's first file). All other URLs pass through. Pure so it unit-tests
   directly.
2. **Name derivation** — explicit `name` arg wins; else `path.Base(u.Path)`.
   If the result is empty, `"/"`, `"raw"`, or `"."` → error
   `cannot derive a name from this URL, pass one explicitly: uc add <url> <name>`.
   Then the existing `validScriptName` + reserved checks on the derived name
   (a URL path can yield `..` or an empty base, so validation is not
   optional here).
3. **Fetch** — `http.Client{Timeout: 30 * time.Second}`; non-200 →
   `"fetch %s: %s"` with the status. Read via
   `io.LimitReader(resp.Body, 1<<20)` (1 MB cap); empty body → error.
4. **Install** — same as the local path: `EnsureDirs()`, write to
   `ScriptsDir/<name>` mode 0o755, print `added <url> -> <dest>` plus one
   extra line: `note: review it before first run — uc which <name>`.
   (uc never auto-executes, so no confirmation prompt; the note keeps the
   trust burden visible.)

### File changes

- `internal/commands/commands.go` — `Add` branch + `rawURL` + a private
  `addFromURL(rawurl, name string) error`.
- `internal/help/help.go` — extend the `"add"` entry with the URL form and
  the gist behavior.
- `helpText` const (commands.go:628 currently reads
  `uc add <path> [name] [--force]` → `uc add <path|url> [name] [--force]`),
  main.go `runAdd` usage string (main.go:98), README table + docs/doc.md
  “Add a script from a URL” subsection, CHANGELOG.

### Edge cases

- Redirects: followed by default (`http.Client`) — correct for
  `gist.github.com/.../raw` → `gist.githubusercontent.com`.
- HTML masquerading as a script (someone passes a repo page URL): no
  content sniffing in v1 — the review note covers it; `uc help add` warns to
  use raw URLs.
- Query strings/fragments in name derivation: `path.Base(u.Path)` ignores
  them by construction.

### Tests

In `commands_test.go`, using `httptest.NewServer` (no network):

- Success: server returns a shebang script → file installed 0o755, output has
  `added` + `review`; name derived from the URL path.
- Explicit name overrides derived name.
- 404 → error, nothing written.
- Un-derivable name (path `/`) without explicit name → error.
- `rawURL` table test: gist page URL → `/raw` appended; gist URL already
  containing `/raw/` unchanged; non-gist URL unchanged; invalid URL errors.

No e2e (would need network); the unit coverage is the contract.

---

## 4. `uc stats` + `uc history search`

### Goal

Surface the invocation data uc already records: a searchable history and a
shareable “most-used commands” view.

### CLI surface

```sh
uc history search <term>    # case-insensitive substring over "name args..."
uc stats                    # per-name counts over the retained window
```

### Design

**`history search`** — new verb inside `runHistory` (main.go:146), before the
numeric-count parse: `args[0] == "search"` requires exactly one further arg
(`usage: uc history search <term>`). Handler
`func (r *Runner) HistorySearch(term string) error`: load history, filter
where `strings.Contains(strings.ToLower(name + " " + strings.Join(args, " ")), strings.ToLower(term))`,
print matches most-recent-first in the **exact** format `History` uses.
Extract the formatting loop of `History` (commands.go:318-331) into a shared
`printEntries(entries []history.Entry, now time.Time)` helper so search and
history render identically; empty result → `no matching history entries`.
Numbering note: printed indices are the entries' positions in the full
history (usable with `uc history run <n>`), not a 1..k renumbering of the
filtered list.

**`stats`** — new reserved subcommand, no arguments
(`usage: uc stats`). Handler `func (r *Runner) Stats() error`:

- Load history; empty → `no history yet` (same message as `History`).
- Count invocations per `Name`, remembering each name's most recent `Kind`
  and timestamp.
- Sort by count desc, then name asc.
- Render aligned columns + a text bar scaled to the max count (bar width 20,
  `strings.Repeat("▇", max(1, count*20/maxCount))`):

```
killport  script    12  ▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇▇
gs        alias      5  ▇▇▇▇▇▇▇▇
deploy    function   1  ▇
based on the last 18 invocations (history_size: 25)
```

- The footer is the honest-scope caveat: history is capped at
  `history_size` (default 25), so stats cover only that window. Docs suggest
  raising `history_size` in settings.json for richer stats. **No new storage**
  in this feature.

### File changes

- `internal/reserved/reserved.go` — `"stats"` in `Names`.
- `cmd/uc/main.go` — `case "stats"` (no-arg check) + `search` verb in
  `runHistory`.
- `internal/commands/commands.go` — `Stats`, `HistorySearch`,
  `printEntries` extraction (pure refactor of `History`).
- `internal/help/help.go` — `"stats"` entry; extend `"history"` entry with
  the `search` verb.
- `helpText`, README table, docs/doc.md History section, CHANGELOG.

### Edge cases

- Ties in count → name-alphabetical for stable output (tests depend on it).
- `Kind` empty on pre-existing history entries → display `script` (same
  fallback as `History`, commands.go:326-329).
- `uc history run` continues to work unchanged; `search` must be checked
  before the `strconv.Atoi` fallthrough so `uc history search 5` searches for
  `"5"` rather than printing 5 entries.

### Tests

`commands_test.go`, seeding history via `history.Append` into a temp
`HistoryPath`:

- `Stats`: three names with counts 3/2/1 → output order, counts, footer
  numbers; empty history → `no history yet`.
- `HistorySearch`: matches on name, on args, case-insensitively; no match →
  `no matching history entries`; indices match `History`'s numbering for the
  same entries.

E2E: run a script twice + an alias once, `uc stats` contains the script name
before the alias name; `uc history search <arg>` finds the run.

---

## 5. Tags

### Goal

Organize a grown collection: `# @tag: git` on scripts, `--tag` on
aliases/functions, `uc list --tag git`, and `#git` filtering in the picker.

### Design

**Metadata (scripts)** — `ExtractMeta` (registry.go:274) learns a repeatable
`@tag` case: value split on `","`, each piece `strings.TrimSpace`d +
`strings.ToLower`ed, empties dropped, appended to `Meta.Tags []string` (dedup
via a small seen-set). `# @tag: git, network` and two separate `@tag:` lines
are equivalent.

**Stores (aliases/functions)** — add `Tags []string` with
`json:"tags,omitempty"` to `aliases.Alias` and `functions.Function`
(forward-compatible: unknown keys are already ignored on load, `omitempty`
keeps old files byte-stable on rewrite). `--tag <t>` repeatable flag parsed
in `runAliasAdd` / `runFunctionAdd` with the same scan-loop shape as
`--desc` (main.go:201-209); tags normalized (trim/lowercase) at parse time.
`AliasAdd`/`FunctionAdd` signatures gain `tags []string`.

**Registry** — `registry.Item` (registry.go:161) and `Resolved`
(registry.go:74) gain `Tags []string`; `List()` and `Resolve()` populate them
for all three kinds.

**`uc list --tag <t>`** — main.go gains a `runList(r, rest)` parser (today
`list` calls `r.List()` directly): accepts optional `--tag <t>`; usage
`usage: uc list [--tag <tag>]`. `Runner.List` gains a `tag string` parameter
(empty = no filter); filtering is exact match against the item's normalized
tags. Listing rows append tags when present, reusing the existing column
alignment:

```
killport  script  kills a process by port  #net #ops
```

Empty filtered result → `no entries tagged %q`.

**Picker** — `item` (picker.go:29) gains `tags []string` (populated in `Run`
from `registry.Item.Tags`). Extract a pure function:

```go
// splitQuery separates "#tag" tokens from the fuzzy pattern.
func splitQuery(input string) (pattern string, tags []string)
```

Tokens starting with `#` (len > 1) become tag prefix-filters; the remaining
tokens re-join (space-separated) as the fuzzy pattern. `applyFilter`
(picker.go:77) keeps an item only if every `#tag` filter prefix-matches at
least one of its tags AND the pattern fuzzy-matches name or desc (unchanged
`fuzzyMatch`). A lone `#` filters nothing (treated as pattern text). Update
the hint line (picker.go:208): `(type to filter, #tag to filter by tag, ...)`.

### File changes

`internal/registry/registry.go`, `internal/aliases/aliases.go`,
`internal/functions/functions.go`, `internal/commands/commands.go`
(`List(tag)`, `AliasAdd`/`FunctionAdd` signatures), `cmd/uc/main.go`
(`runList`, flag scans), `internal/picker/picker.go` (`splitQuery`,
`applyFilter`, item struct, hint), help (`list`, `alias`, `function`
entries), README (tags in the metadata section + list row), docs/doc.md
(new Tags section), CHANGELOG.

### Edge cases

- Picker arg-mode is untouched — `#` only has meaning in filter mode input.
- `uc list --tag` with no value → usage error.
- Tag display order: as stored (scripts: file order; aliases/functions: the
  order given), no sorting — surgical.
- Shadowing/dedup in `List()` is unchanged; the winning kind's tags win.

### Tests

- `registry_test.go`: `@tag` parsing (comma splitting, lowercase, dedup,
  repeatable lines); `List` carries tags.
- `aliases_test.go` / `functions_test.go`: tags round-trip through the JSON
  store; old files without `tags` load cleanly.
- New `picker` test alongside `picker_test.go`: `splitQuery` table test
  (`"#git kill"` → pattern `kill`, tags `[git]`; `"#"` → pattern `#`; multiple
  tags) + `applyFilter` behavior through a constructed model.
- `commands_test.go`: `List("git")` filters; row includes `#git`.
- E2E: add a `@tag`-ged script + `--tag`-ged alias, `uc list --tag` shows
  exactly the right entries.

---

## 6. Env injection

### Goal

Let an entry carry its own environment (`AWS_PROFILE=prod` etc.) without a
wrapper script: `# @env: KEY=value` tags on scripts, `--env KEY=value` on
aliases/functions.

### Design

**Metadata (scripts)** — `ExtractMeta` learns repeatable `@env`:
`# @env: AWS_PROFILE=prod`. The value is kept as a raw `KEY=value` string —
exactly the shape `exec` env wants. Lines whose value lacks `=` or has an
empty key are silently ignored (consistent with the lenient tag parsing of
decision 20). `Meta.Env []string`, preserving file order (later lines win
naturally at exec time).

**Stores (aliases/functions)** — `Env map[string]string` with
`json:"env,omitempty"` on both structs; repeatable `--env KEY=value` flag on
`alias add` / `function add` (value validated to contain `=` at parse time —
hard error here, unlike lenient script tags, because the user is typing it
interactively). Rendered to `KEY=value` strings (sorted by key for
determinism) when threading to the executor.

**Registry** — `Resolved.Env []string`; populated from `Meta.Env` for
scripts and from the store maps for aliases/functions.

**Executor** — the three run functions and the chokepoint gain an env
parameter:

```go
func Run(path string, args []string, extraEnv []string) error          // executor.go:56
func RunAlias(command, name string, args, extraEnv []string) error     // executor.go:96
func RunFunction(body, name string, args, extraEnv []string) error     // executor.go:86
func runBash(script, name string, args, extraEnv []string) error       // executor.go:100

// exec1 is per-GOOS — change both or the Windows build breaks:
func exec1(bin string, argv, extraEnv []string) error   // exec_unix.go:12 — env := append(os.Environ(), extraEnv...)
func exec1(bin string, argv, extraEnv []string) error   // exec_windows.go:18 — forwards to runChild, which already takes an env slice (exec_windows.go:30)
```

Appending after `os.Environ()` means the entry's env **overrides** the
session's (last occurrence wins in `execve` env lookup for libc and shells).
`ExecReplace` is unchanged (it has no entry context).
`execResolved` (commands.go:60) threads `resolved.Env` into each call. Note
the executor now has per-GOOS files (`exec_unix.go` / `exec_windows.go`) —
the env parameter has to be added to **both** implementations of the exec
chokepoint, not just the Unix one, or the Windows build breaks.

**No settings.json involvement** — env lives next to the entry it belongs to
(metadata/store), not in config.

### File changes

`internal/registry/registry.go`, `internal/executor/executor.go`,
`internal/aliases/aliases.go`, `internal/functions/functions.go`,
`internal/commands/commands.go` (`execResolved`, `AliasAdd`/`FunctionAdd`
signatures again — land feature 5 first so the signature churn happens once
per function total), `cmd/uc/main.go` (flag scans), help entries, README +
docs/doc.md (Metadata + Aliases/Functions sections), CHANGELOG.

### Edge cases

- `@env` with `$`-references (`# @env: PATH=/opt/bin:$PATH`): **not**
  expanded for scripts (raw execve env — document this). For
  aliases/functions the value passes through `bash -c`'s environment the same
  literal way; expansion inside the alias command itself still works
  normally. Keep semantics: literal values only, documented.
- Secrets in `@env` lines end up in plain files — add a doc note (same trust
  model as the script body itself).
- `uc history run` and picker paths flow through `execResolved` → env applies
  everywhere automatically.

### Tests

- `registry_test.go`: `@env` parsing — valid, missing `=`, empty key, order
  preserved; `Resolve` carries env for all three kinds.
- The exec chokepoint itself is process-replacing and so mostly untestable
  in-process; `internal/executor/executor_test.go` only covers the
  pre-exec failure paths (interpreter lookup, missing `bash`), so keep the
  env behaviour covered by e2e:
  a script `echo "$FOO"` with `# @env: FOO=bar` → output `bar`; an alias
  added with `--env FOO=baz` printing `$FOO` → `baz`; session override check
  (`FOO=session` in the container env, entry env wins).
- `commands_test.go`: flag parse errors for `--env` without `=`.

---

## 7. `uc sync` — git-backed sync (no server)

### Goal

Sync scripts/aliases/functions across machines using a git remote **the user
owns** (private GitHub repo etc.). uc shells out to `git`; there is no hosted
backend.

### CLI surface

```sh
uc sync init <remote-url>   # once per machine
uc sync push                # publish local state
uc sync pull                # adopt remote state
uc sync status              # remote, dirtiness, ahead/behind
```

### Design

**Layout — staging repo.** The clone lives at `$UC_HOME/sync`
(`Config.SyncDir()` helper, config.go). Repo contents:

```
scripts/          # mirror of $UC_HOME/scripts
aliases.json      # copy of $XDG_CONFIG_HOME/uc/aliases.json
functions.json    # copy of $XDG_CONFIG_HOME/uc/functions.json
```

Excluded by design: `history.json` (per-machine noise, constant conflicts)
and `settings.json` (machine-specific, e.g. `editor`). The remote URL lives
in the repo's own git config — **no new settings.json key**. The staging
layout (rather than making the live dirs a repo) is what lets a single repo
span the two live trees (`UC_HOME` vs `ConfigDir`) and keeps `.git` out of
the live directories.

**New package `internal/gitsync`** — named to avoid clashing with stdlib
`sync`. Surface:

```go
type Syncer struct {
    Dir         string      // $UC_HOME/sync
    ScriptsDir  string      // live scripts
    AliasesPath string      // live aliases.json
    FunctionsPath string    // live functions.json
    Out         io.Writer
}

func New(cfg *config.Config, out io.Writer) *Syncer
func (s *Syncer) Init(remoteURL string) error
func (s *Syncer) Push() error
func (s *Syncer) Pull() error
func (s *Syncer) Status() error
```

Commands handlers stay thin (`func (r *Runner) SyncInit(url string) error`
etc. just construct and delegate).

**Git plumbing** — one private helper:
`func (s *Syncer) git(args ...string) (string, error)` running
`exec.Command("git", append([]string{"-C", s.Dir}, args...)...)`, capturing
combined output; on failure the error includes git's output. `Init` is the
exception (no repo yet): plain `git clone <url> <dir>`. A missing `git`
binary (`exec.LookPath`) → `git not found on PATH — uc sync requires git`.
Never `ExecReplace` — uc must regain control after every git call.

**Mirror semantics** (deletions propagate; the core correctness rule):
*push treats live as truth, pull treats repo as truth.*

```go
// mirror makes dst's managed set identical to src's: scripts/ plus the two
// JSON files. Returns a per-file change list for printing.
func mirror(src, dst files) ([]change, error)
```

- Scripts: copy every regular file `srcScripts → dstScripts` with mode
  0o755; delete files in `dstScripts` absent from `srcScripts`. Non-regular
  entries (dirs, symlinks) are skipped with a warning.
- JSON files: copy when the source exists; delete the destination copy when
  the source doesn't. (On pull into the live side, "delete aliases.json"
  means all aliases were removed on the other machine — correct under mirror
  semantics.)
- Print a summary: `+ scripts/killport`, `~ aliases.json`, `- scripts/old`
  (`~` = content changed, compared by bytes). No output → `nothing to sync` /
  `already up to date`.

**Subcommand flows:**

- `Init(url)`: error if `s.Dir/.git` exists (`already initialized — %s`).
  `git clone`. Then: repo **non-empty** (any of the three managed paths
  present) → mirror repo → live and print `pulled existing state from
  <url>`; repo **empty** → `EnsureDirs`, mirror live → repo, `git add -A`,
  commit `initial sync from <hostname>`, `git push -u origin HEAD`, print
  `pushed initial state to <url>`.
- `Push()`: not initialized → `run uc sync init <remote-url> first`. Mirror
  live → repo; `git add -A`; `git status --porcelain` empty → `nothing to
  sync`, done. Else commit `sync from <hostname> at <RFC3339>` and
  `git push`; a rejected push (non-fast-forward, detect via git's output)
  → error `remote has newer changes — run uc sync pull first`.
- `Pull()`: `git pull --ff-only`; failure → error `sync repo has diverged —
  resolve manually in <dir>, then run uc sync pull again`. Then mirror
  repo → live, print the change summary (or `already up to date`).
- `Status()`: `git remote get-url origin`; `git fetch` (tolerate failure
  with a warning — offline status should still work); dirtiness via
  `git status --porcelain` **after** a live→repo dry mirror comparison
  (report `local changes not yet pushed` by diffing live vs repo with the
  same comparison `mirror` uses, without writing); ahead/behind via
  `git rev-list --left-right --count @{u}...HEAD`.

### File changes

- New `internal/gitsync/gitsync.go` (+ `gitsync_test.go`).
- `internal/config/config.go` — `func (c *Config) SyncDir() string`
  (`filepath.Join(c.UCHome, "sync")`).
- `internal/reserved/reserved.go` — `"sync"` in `Names`.
- `cmd/uc/main.go` — `case "sync"` → `runSync` (verb switch shaped like
  `runAlias`; `init` requires exactly one URL arg; `push`/`pull`/`status`
  take none): `usage: uc sync <init|push|pull|status> ...`.
- `internal/commands/commands.go` — four thin handlers, `helpText` block.
- `internal/help/help.go` — `"sync"` entry documenting layout, exclusions,
  and the conflict story.
- README (Commands table + a Sync section + file-layout diagram gains
  `sync/`), docs/doc.md (new top-level Sync section), CHANGELOG.
- E2E infra: install git in the container (an exec step
  `apt-get update && apt-get install -y git` during container setup in
  `e2e_test.go`, or bake a derived image — prefer the exec step, zero new
  files).

### Edge cases

- `$UC_HOME/sync` inside `$UC_HOME` must **not** be treated as a script —
  `listScripts` (registry.go:222) only reads `ScriptsDir`, so it's already
  safe; verify with a test.
- First `Push` on a machine where `init` cloned non-empty: fine — push just
  mirrors and commits the delta.
- Hostname failure (`os.Hostname`) → fall back to `"unknown"` in commit
  messages.
- Git identity not configured (fresh machine): commit will fail; surface
  git's own message (it's self-explanatory) — no uc-side identity handling
  in v1.
- Public-repo secrets: docs prominently recommend a **private** repo; no
  URL heuristics in v1.
- Concurrent pushes from two machines: second push errors with the
  pull-first message; `pull --ff-only` then replays — last writer wins per
  file under mirror semantics, which is the documented v1 conflict story.

### Tests

`gitsync_test.go` — all against a **local bare repo** (`git init --bare` in
`t.TempDir()`), no network; skip gracefully (`t.Skip`) if `git` is absent:

- init(empty remote) from home A with scripts+aliases → bare repo gains the
  files; init on home B (same remote) → files appear in B's live dirs, exec
  bit preserved.
- push propagates adds/updates/deletes (delete a script on A, push, pull on
  B → gone on B).
- push with nothing changed → `nothing to sync`, no new commit.
- pull with diverged history (commit directly into B's staging repo, push
  from A) → ff-only error message.
- `mirror` unit tests directly: add/update/delete detection, mode 0o755,
  skips subdirectories.
- init twice → `already initialized`.

E2E (`cli_test.go`, after installing git in the container): two sandboxes
(`sandbox(t)` twice) sharing one bare repo path inside the container —
add script on A, `sync init` + implicit push; `sync init` on B → `uc <name>`
works on B; remove on A, push; pull on B → exit 127 on B.

---

## Commit sequence

One `feat:` commit per feature, in the order above; each leaves
`gofmt -l .` clean and `make vet && make build && make test` green. Feature 5
(tags) lands before feature 6 (env) so the `AliasAdd`/`FunctionAdd` signature
churn is reviewed once per feature, not re-shuffled.

```
feat: implememt fish completion                      # done — 7c51c01 (PR #8)
feat: uc new — scaffold a script into the registry   # done — feature/add-script-editor
feat: uc add <url> — register a script from a URL or gist   # next
feat: uc stats and uc history search
feat: tags — @tag metadata, uc list --tag, #tag picker filtering
feat: per-entry env injection via @env tags and --env flags
feat: uc sync — git-backed sync across machines
```

## End-to-end verification

After each feature and again at the end:

1. `gofmt -l .` empty, `make vet`, `make build`, `make test` (mirrors CI).
2. `make test-e2e` (Docker; container suite including the new lifecycle
   tests; git install step added by feature 7).
3. Manual smoke on `dist/uc` with `UC_HOME` pointed at a scratch dir:
   `uc completion fish | head`, `uc new t1` happy + abort paths
   (`EDITOR=true` aborts), `uc add http://localhost:.../x.sh` against a
   throwaway `python3 -m http.server`, `uc stats`, tag + env round-trips,
   and a two-home sync round-trip against a local bare repo.
