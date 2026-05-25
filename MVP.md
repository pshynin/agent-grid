# MVP.md — AgentGrid v0.1 scope

This document is the contract for the first usable version of AgentGrid.
Anything not listed here is out of scope until v0.1 ships and the core loop is
validated.

## 1. MVP command list

Only these commands ship in v0.1.

```
agentgrid init

agentgrid agent add  --name <slug> --task "..." --branch <br>
                     [--base <br>] [--worktree <path>]
                     [--claim <kind:pattern:intent>]...
agentgrid agent list [--json]
agentgrid agent show <agent> [--json]

agentgrid claim add   <agent> <kind:pattern:intent> [<...>]
agentgrid claim list  [--agent <agent>] [--json]
agentgrid claim check <kind:pattern:intent>

agentgrid status     [--json]
agentgrid refresh    [--agent <agent>]
agentgrid stale      [--json]
agentgrid diff-risk  <agent> [--json] [--no-refresh]
```

Notes:

- `agent add` records the branch and (optional) worktree path the user has
  already created. AgentGrid does **not** create branches or worktrees in
  v0.1.
- `agent add` captures `base_commit = merge-base(branch, base)` at
  registration time. Stale detection needs this.
- `agent add` accepts repeated inline `--claim` flags for the common case
  of registering an agent and its scope in one step. Equivalent to running
  `claim add` afterwards.
- No `rm`, no `release`, no `resolve` in v0.1. Resetting state means wiping
  `.agentgrid/state.db`. This is documented; management commands wait until
  the core loop is validated.
- Claim kinds in v0.1: `path` (literal) and `glob` (doublestar) only.
  `module` is deferred — it's pure sugar over a config map.
- Claim intents in v0.1: `edit` and `read`. `create` and `delete` collapse
  into `edit` for overlap purposes.

## 2. MVP SQLite tables

Five tables. That's it.

```sql
schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)

agents(
  id            TEXT PRIMARY KEY,        -- ULID
  name          TEXT NOT NULL UNIQUE,
  task          TEXT NOT NULL,
  branch        TEXT NOT NULL,
  base_branch   TEXT NOT NULL,
  base_commit   TEXT NOT NULL,
  worktree_path TEXT,                    -- optional, informational
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
)

claims(
  id         TEXT PRIMARY KEY,
  agent_id   TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL,              -- path|glob
  pattern    TEXT NOT NULL,
  intent     TEXT NOT NULL,              -- edit|read
  created_at TEXT NOT NULL
)
CREATE INDEX idx_claims_agent ON claims(agent_id);

diff_snapshots(
  id               TEXT PRIMARY KEY,
  agent_id         TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  head_commit      TEXT NOT NULL,
  files_changed    INTEGER NOT NULL,
  lines_added      INTEGER NOT NULL,
  lines_removed    INTEGER NOT NULL,
  touched_files    TEXT NOT NULL,        -- JSON array
  forbidden_hits   TEXT NOT NULL,        -- JSON array
  claim_violations TEXT NOT NULL,        -- JSON array
  risk_level       TEXT NOT NULL,        -- low|medium|high
  risk_reasons     TEXT NOT NULL,        -- JSON array
  taken_at         TEXT NOT NULL
)
CREATE INDEX idx_diffs_agent ON diff_snapshots(agent_id, taken_at DESC);

stale_marks(
  id                TEXT PRIMARY KEY,
  agent_id          TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  reason            TEXT NOT NULL,
  conflicting_files TEXT NOT NULL,       -- JSON array
  recommendation    TEXT NOT NULL,       -- rebase|re-plan|review|narrow
  created_at        TEXT NOT NULL
)
CREATE INDEX idx_stale_agent ON stale_marks(agent_id, created_at DESC);
```

Cut from the full schema in `SCHEMA.md`: `events`, `meta`, `worktrees`,
`reviews`, `prs`, mutable `agents.status`, claim lifecycle columns, stale
resolution columns, snapshot module/test columns. Derived where possible,
deferred where not.

## 3. MVP package structure

```
agent-grid/
├── cmd/agentgrid/main.go
├── internal/
│   ├── cli/                           # one file per command
│   │   ├── root.go
│   │   ├── init.go
│   │   ├── agent.go                   # add | list | show
│   │   ├── claim.go                   # add | list | check
│   │   ├── status.go
│   │   ├── refresh.go
│   │   ├── stale.go
│   │   └── diffrisk.go
│   ├── core/                          # pure types
│   │   ├── agent.go
│   │   ├── claim.go
│   │   ├── diff.go
│   │   └── stale.go
│   ├── policy/                        # pure, table-tested
│   │   ├── overlap.go
│   │   ├── stale.go
│   │   └── diffrisk.go
│   ├── store/
│   │   ├── store.go                   # interface + SQLite impl
│   │   ├── migrations.go              # embed + apply
│   │   └── migrations/0001_init.sql
│   ├── git/
│   │   └── git.go                     # exec wrapper
│   └── config/
│       └── config.go
├── go.mod
├── Makefile
└── .github/workflows/ci.yml
```

No `internal/usecase/` layer — folded into `internal/cli/` for v0.1 since
the CLI is the only consumer. Re-introduce if a TUI or HTTP layer is added.

## 4. MVP implementation order

Eight tasks, each a small landable PR. Stop at task 8 and ship `v0.1.0`.

1. **Bootstrap.** `go.mod`, Makefile, CI, empty Cobra binary that prints
   `agentgrid` and exits 0.
2. **`core` + `store` + `init`.** Define `Agent`, `Claim`, `DiffSnapshot`,
   `StaleMark` structs. SQLite store with embedded migration
   `0001_init.sql`. `agentgrid init` creates `.agentgrid/` and applies
   migrations. Integration test against `t.TempDir()`.
3. **`config` + defaults.** Load `.agentgrid/config.yaml`; write a
   commented default on `init`. Schema: `default_base_branch`,
   `forbidden_paths[]`, `test_file_globs[]`, `diff_risk.thresholds`.
4. **`policy.overlap` + tests.** Table-driven tests covering path/glob ×
   edit/read combinations. Pure functions, zero I/O.
5. **`agent add` / `claim add` / `claim check` / `agent list` / `agent
   show` / `claim list`.** Wire CLI → policy → store. `agent add` shells
   out to `git rev-parse` + `git merge-base` to capture `base_commit`.
   Hard overlap exits `3`. Integration tests against a temp DB and a
   temp git repo.
6. **`internal/git` adapter.** Formalize `RevParse`, `MergeBase`,
   `DiffNameOnly`, `DiffNumstat`, `IsAncestor`. Integration tests on a
   fixture repo.
7. **`policy.stale` + `refresh` + `stale`.** For each agent, compare files
   changed on the base branch since the agent's captured `base_commit`. If
   those files overlap the agent's claims or already-modified files, mark
   the agent stale. Surface in `agentgrid stale`.
8. **`policy.diffrisk` + `diff-risk` + `status`.** Score with the reason
   table from `ALGORITHMS.md`. Persist snapshot. `status` shows one row per
   agent with risk, stale flag, and merged flag (derived via
   `IsAncestor`).

After task 8: ship `v0.1.0` as a `go install`-able binary. No goreleaser, no
Homebrew yet. If users care, distribution gets its own follow-up.

## 5. Explicitly deferred

Each entry: why it's safe to cut now, and what triggers picking it up.

- **`spawn` (worktree provisioning).** `git worktree add` is one well-known
  command. *Revisit:* when setup friction shows up in feedback.
- **`agent rm`, `claim release`.** A few-day session doesn't need cleanup;
  wiping `state.db` resets cleanly. *Revisit:* when claims accumulate past
  ~10 active per repo.
- **`agentgrid cleanup`.** Worktrees are user-created, user-removed in v0.1.
  *Revisit:* after `spawn` lands.
- **`agentgrid pr` and GitHub integration.** PR body can be rendered
  manually from `diff-risk --json`. *Revisit:* after the core loop is
  trusted; pick up with a `gh` shell-out.
- **Reviewer / QA / security agents.** AgentGrid coordinates, doesn't
  review. *Revisit:* possibly never; integrate via ingestion only.
- **Tmux integration.** Users run their own sessions; AgentGrid doesn't
  need to know. *Revisit:* after repeated requests; read-only metadata
  first.
- **TUI.** CLI + `--json` + `watch agentgrid status` is enough.
  *Revisit:* after the CLI proves the loop.
- **Remote dashboard / multi-machine sync.** Single-tenant local tool is
  the thesis. *Revisit:* likely never.
- **AI / model integration.** AgentGrid is model-agnostic by design.
  *Revisit:* never.
- **`events` audit table.** `slog` file log covers debugging needs.
  *Revisit:* when a UI surfaces history.
- **`module` claim kind.** `glob:pkg/billing/**` works fine.
  *Revisit:* when users complain about repeated glob typing.
- **`status` filters and buckets.** A single flat table is readable for
  fewer than ~10 agents. *Revisit:* when users run more than that at once.
- **Claim expiration.** Manual hygiene in v0.1.
  *Revisit:* after long-running sessions become common.
- **Forbidden-path claim refusal.** Detected in diff-risk; not enforced at
  claim time. *Revisit:* after a user actually gets bitten.
- **`worktrees` table.** Worktree path is informational; one column on
  `agents` is enough. *Revisit:* when AgentGrid creates worktrees itself.
- **Stale-mark history.** Recompute and overwrite is simpler and correct.
  *Revisit:* when users want a timeline view.
- **`goreleaser`, Homebrew, signed binaries.** `go install` works for early
  adopters. *Revisit:* after v0.1 has external users.

## 6. Acceptance criteria for v0.1

The MVP is "first-usable" if every one of these passes on a clean macOS or
Linux machine with `git >= 2.20`.

**Setup**

- `go install` produces a single `agentgrid` binary.
- `agentgrid init` inside a fresh git repo creates
  `.agentgrid/{config.yaml,state.db,agentgrid.log}` and exits `0`.
  Re-running is idempotent.

**Claim-before-touch**

- `agent add` with one or more inline `--claim` flags succeeds and `agent
  show` round-trips the recorded claims.
- A second `agent add` with an overlapping `edit` claim exits `3` naming
  the conflicting agent and pattern; nothing is written to the DB. Probing
  the same claim via `claim check` returns the same conflict without side
  effects.
- Two agents with overlapping `read` claims both succeed.

**Stale detection**

- For each agent, AgentGrid compares files changed on the base branch
  since the agent's captured `base_commit`. If those files overlap the
  agent's claims or already-modified files, the agent is marked stale.
- Scenario: agents A and C branch off `main` at commit `X`. A is merged
  into `main` and changes `pkg/billing/types.go`. C has claimed
  `glob:pkg/billing/**:read`. After `agentgrid refresh`, `agentgrid stale`
  lists C with the conflicting file path and a recommendation.
- A subsequent `refresh` after C rebases past the change clears C from the
  stale list.

**Diff-risk**

- An agent with 40 files / 2,500 lines changed and a touched file under
  `vendor/**` scores `HIGH` with reasons including `files_over_high`,
  `lines_over_medium`, and `forbidden_path_touched`. JSON output contains
  the full reasons array.
- An agent with 3 files / 50 lines, all inside its claim, scores `LOW`
  with no reasons.
- An agent that modifies a file outside any of its claims has
  `claim_violation` in its reasons.

**Status**

- `agentgrid status` prints one row per registered agent with: name,
  branch, ahead/behind base, last risk level, stale flag, merged flag
  (derived from `git merge-base --is-ancestor`).
- `agentgrid status --json` produces a stable schema documented in
  `CLI_SPEC.md`.

**Behavioral**

- All commands complete in under 200 ms on a repo with ≤ 5 agents and
  ≤ 10k files (excluding `git diff` time).
- Exit codes match the contract: `0` ok, `1` user error, `2` system
  error, `3` policy refusal.
- The binary makes no network calls. `agentgrid --help` mentions no
  remote feature.
- Wiping `.agentgrid/state.db` and re-running `init` cleanly resets state
  without touching git.

**Quality bar**

- `internal/policy/` has table-driven tests; overlap and diff-risk reach
  100% line coverage. Stale logic is tested end-to-end against a temp git
  repo.
- `go vet`, `golangci-lint run`, and `go test ./...` are all clean in CI
  on macOS and Linux.
