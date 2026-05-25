# IMPLEMENTATION_PLAN.md — AgentGrid

## 1. Recommended Go project structure

```
agent-grid/
├── cmd/
│   └── agentgrid/
│       └── main.go                # cobra root, wires commands
├── internal/
│   ├── cli/
│   │   ├── root.go
│   │   ├── init.go
│   │   ├── agent.go               # agent add/list/show/rm
│   │   ├── claim.go               # claim add/list/release/check
│   │   ├── spawn.go
│   │   ├── status.go
│   │   ├── refresh.go
│   │   ├── stale.go
│   │   ├── diffrisk.go
│   │   ├── review.go
│   │   ├── pr.go
│   │   ├── cleanup.go
│   │   └── format/                # human + json renderers
│   ├── core/                      # pure domain types
│   │   ├── agent.go
│   │   ├── claim.go
│   │   ├── worktree.go
│   │   ├── diff.go
│   │   ├── event.go
│   │   └── status.go
│   ├── usecase/                   # orchestrates store + policy + adapters
│   │   ├── agent_add.go
│   │   ├── claim_add.go
│   │   ├── spawn.go
│   │   ├── refresh.go
│   │   ├── diff_risk.go
│   │   ├── pr.go
│   │   └── cleanup.go
│   ├── policy/                    # pure functions, heavily tested
│   │   ├── overlap.go
│   │   ├── stale.go
│   │   ├── diffrisk.go
│   │   ├── forbidden.go
│   │   ├── pr.go
│   │   └── status.go
│   ├── store/
│   │   ├── store.go               # Store interface
│   │   ├── sqlite.go              # implementation
│   │   ├── migrations/            # *.sql, embedded
│   │   └── queries.go             # SQL constants / sqlc output
│   ├── git/
│   │   ├── git.go                 # exec wrapper
│   │   ├── worktree.go
│   │   └── diff.go
│   ├── tmux/
│   │   └── tmux.go
│   ├── gh/
│   │   └── gh.go                  # gh CLI wrapper
│   ├── config/
│   │   ├── config.go
│   │   └── defaults.go
│   └── log/
│       └── log.go
├── docs/
│   ├── adr/
│   └── demos/
├── templates/
│   └── pr.md.tmpl                 # embedded default
├── test/
│   └── fixtures/
├── .github/workflows/ci.yml
├── .goreleaser.yaml
├── Makefile
├── go.mod
├── go.sum
├── PRODUCT.md
├── ARCHITECTURE.md
├── ROADMAP.md
├── IMPLEMENTATION_PLAN.md
├── SCHEMA.md
├── ALGORITHMS.md
├── CLI_SPEC.md
└── README.md
```

### Why this layout

- `cmd/agentgrid` stays tiny — wiring only.
- `internal/cli` holds command handlers but no business logic.
- `internal/usecase` is where the "for each agent, do X" loops live; this is
  what a future TUI or HTTP layer will reuse.
- `internal/policy` is pure — no I/O, no shell — so every rule is unit-testable
  with table-driven tests.
- `internal/store` is the only place that touches SQL.
- `internal/git`, `internal/tmux`, `internal/gh` are the only places that
  shell out.

## 2. Module / package boundaries

- `core` depends on stdlib only.
- `policy` depends on `core` only.
- `store`, `git`, `tmux`, `gh`, `config`, `log` depend on `core` (and their
  own driver libs).
- `usecase` depends on `core`, `policy`, and adapter interfaces.
- `cli` depends on `usecase` and `format`.

The dependency arrows always point inward (`cli → usecase → policy/store/...
→ core`). No cycles.

## 3. CLI command design (MVP set)

See `CLI_SPEC.md` for full UX. The MVP command tree:

```
agentgrid init
agentgrid agent  add | list | show | rm
agentgrid claim  add | list | release | check
agentgrid spawn  [--from <agent>] [--worktree <path>] [--base <branch>]
agentgrid status [--bucket <b>] [--json]
agentgrid refresh
agentgrid stale
agentgrid diff-risk <agent>
agentgrid review
agentgrid pr <agent> [--open] [--dry-run] [--force]
agentgrid cleanup [--yes]
agentgrid help
agentgrid version
```

Cross-command conventions:

- `<agent>` accepts `name` or `id` (short prefix match acceptable).
- `--repo` defaults to the git toplevel of `cwd`.
- `--db` defaults to `<repo>/.agentgrid/state.db`.
- `--json` and `--quiet` are universal.

## 4. SQLite schema (overview)

Full DDL in `SCHEMA.md`. Tables:

- `agents`
- `claims`
- `worktrees`
- `diff_snapshots`
- `stale_marks`
- `events`
- `reviews`
- `prs`
- `meta`
- `schema_migrations`

Indexes mostly on `(agent_id)`, `(status)`, and `(created_at)`. Foreign keys
on. WAL mode. `PRAGMA busy_timeout = 5000`.

## 5. Config file format

`.agentgrid/config.yaml`:

```yaml
version: 1

repo:
  root: "."                      # resolved at init; informational
  default_base_branch: main

worktrees:
  root: ".agentgrid/worktrees"   # relative to repo root
  prefix: ""                     # optional prefix for branch names

claims:
  default_expiration: 72h        # claims auto-expire after this

diff_risk:
  thresholds:
    files_low: 5
    files_medium: 15
    files_high: 30
    lines_low: 200
    lines_medium: 600
    lines_high: 1500
    modules_medium: 3
    modules_high: 5
  test_file_globs:
    - "**/*_test.go"
    - "**/test/**"
    - "**/__tests__/**"
  forbidden_paths:
    - "vendor/**"
    - "node_modules/**"
    - "migrations/**"
    - "infra/prod/**"
  require_force_at: high         # high|medium|low

modules:                          # optional path -> module name map
  pkg/billing/**: billing
  pkg/auth/**: auth
  internal/invoice/**: invoice

pr:
  template_path: ".agentgrid/templates/pr.md.tmpl"  # falls back to embedded
  default_open: false

integrations:
  github_cli: true               # use `gh`
  tmux: false                    # opt-in
```

YAML is chosen for nested lists and human edits. The schema is versioned and
validated on load.

## 6. Algorithms (summary)

See `ALGORITHMS.md`. Implementation notes:

- **Overlap:** normalize each claim pattern with `doublestar.Match`
  (`github.com/bmatcuk/doublestar/v4`). Conflict if pattern intersection is
  non-empty AND at least one claim is an `edit`/`create`/`delete`.
  Read-vs-read is allowed.
- **Stale:** for each agent A with base commit `B_A`, compute
  `changed = git diff --name-only <B_A>..<base_branch>`. If any file in
  `changed` matches A's claim patterns or appears in A's already-modified
  set, A is stale. Use merge-base, not raw tip.
- **Diff-risk:** numeric scoring with bucket thresholds; reasons are
  enumerated rather than weighted to a single number.
- **Forbidden path:** glob match per `config.forbidden_paths`.
- **Status:** decision table over (agent.status, has_pr, stale?, risk,
  branch_clean?, ahead/behind).

## 7. Testing strategy

- **Policy:** table-driven tests, 100% line coverage target. Fuzz the
  glob-intersection helper.
- **Store:** integration tests with a real SQLite in `t.TempDir()`. No mocks.
- **Git adapter:** integration tests that `git init` a temp repo, make
  commits, call adapter functions. Skipped if `git` is unavailable.
- **Use cases:** wired with real store + real git adapter against temp repos.
  Avoid mocking; adapters are cheap.
- **CLI:** golden-file tests for `--json` output. Human output is smoke-tested
  only.
- **No mocks of internal packages.** If something is hard to test without
  mocks, it likely belongs in `policy/`.
- CI matrix: `go 1.22`, `go 1.23` on macOS and Linux. Windows is best-effort,
  not blocking.

## 8. Error handling

- Errors wrap with `fmt.Errorf("op: %w", err)` at boundaries.
- Sentinel errors in `core/errors.go`: `ErrOverlap`, `ErrUnknownAgent`,
  `ErrDirtyWorktree`, `ErrNoBaseBranch`, etc.
- CLI prints `error: <message>` to stderr, exits with the appropriate code
  (see `ARCHITECTURE.md` §6).
- `--verbose` enables stack-traces; default is one-line errors.
- No `panic` outside of programmer-error guards (e.g., impossible enum value).

## 9. Logging

- `log/slog` JSON handler to `.agentgrid/agentgrid.log`.
- Default level `warn`; `--verbose` raises to `debug`.
- Every command logs a single `command.start` and `command.end` event with
  duration and exit code. This file is the first thing a user attaches to a
  bug report.
- No PII, no command-line arguments containing user paths logged at `info`
  or above unless explicitly opted in.

## 10. Installation and distribution

- `go install github.com/<user>/agent-grid/cmd/agentgrid@latest` works from
  Phase 1.
- `goreleaser` publishes macOS (arm64, amd64) and Linux (amd64, arm64)
  binaries on tag from Phase 2.
- Homebrew tap (`brew install <user>/agentgrid/agentgrid`) added by Phase 6.
- The binary is self-contained except for runtime deps `git`, `tmux`
  (optional), `gh` (optional). Missing optional deps degrade gracefully with
  a clear message.

## 11. First 10 implementation tasks (in order)

These are the first ten chunks of work after the docs are accepted. Each is
sized to land as a small PR.

1. **Repo bootstrap.** Initialize `go.mod`, add `Makefile`,
   `golangci-lint.yaml`, `.github/workflows/ci.yml`, license, empty
   `cmd/agentgrid/main.go` that prints `agentgrid` and exits 0.
2. **`core` types.** Define `Agent`, `Claim`, `Worktree`, `DiffSnapshot`,
   `Event`, `StatusBucket` with zero behavior; just structs + enums.
3. **`store` skeleton.** Add `Store` interface, SQLite implementation, embedded
   migration `0001_init.sql` covering all MVP tables. `agentgrid init`
   creates `.agentgrid/` and the DB.
4. **`config` loader.** Read `.agentgrid/config.yaml` with defaults; validate
   schema version; surface friendly errors. Wire `agentgrid init` to write a
   commented default file.
5. **`policy.overlap`.** Implement claim-overlap algorithm + table-driven
   tests. Cover path, module, and glob claims with edit/read intents.
6. **`agentgrid agent add` and `claim add`.** Wire CLI → usecase → policy →
   store. Block on overlap. Cover with integration tests against a temp DB.
7. **`agentgrid status` (minimal).** List agents and their claims; `--json`
   output. No git yet.
8. **`internal/git` adapter.** `RevParse`, `MergeBase`, `DiffNameOnly`,
   `DiffNumstat`, `WorktreeAdd`, `WorktreeRemove`, `IsClean`. Integration
   tests against a temp repo.
9. **`agentgrid spawn` and worktree linkage.** Create branch + worktree;
   record in `worktrees`. `agent rm --with-worktree` removes safely.
10. **`policy.stale` + `agentgrid refresh` + `agentgrid stale`.** Compute
    staleness across all agents; persist marks; surface in `status` and via
    `stale` command. End-to-end demo recorded in `docs/demos/phase3.md`.

After this, the remaining MVP work is Phase 4 (diff-risk), Phase 5 (buckets),
and Phase 6 (PR). Each is a similar-sized chunk.

## 12. Open questions to resolve early

These are deliberately not pre-decided in this plan; they should be settled
inside Phase 1 with a one-page ADR each:

- **Glob library.** `doublestar/v4` vs hand-rolled. Probably `doublestar`.
- **Module identity.** Path globs only vs explicit module names in config.
  Plan currently supports both; revisit when overlap UX shows a sharp edge.
- **Claim expiration.** Soft warn vs hard release at expiration time.
- **`refresh` cost.** Acceptable wall-clock budget for 10 agents on a
  100k-file repo. Set a budget and measure in Phase 3.
- **JSON schema versioning.** Bump on every field change vs only on
  breaking. Likely the latter, but document the rule.
