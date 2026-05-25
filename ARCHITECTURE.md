# ARCHITECTURE.md — AgentGrid

## 1. Design principles

1. **Local first.** A single Go binary plus a SQLite file under `.agentgrid/`.
   No daemon in the MVP. No server. No cloud.
2. **Shell out, don't reimplement.** `git`, `tmux`, and `gh` are excellent
   tools. AgentGrid orchestrates them; it does not replace them.
3. **Pull-based by default.** The user runs `status` / `refresh` / `diff-risk`
   when they want fresh information. Background watchers are V1+.
4. **State is a fact log.** Every meaningful action writes an `events` row.
   The current view of the world is a projection over the log. This makes the
   system debuggable with `sqlite3` alone.
5. **Boundary, not framework.** AgentGrid does not embed prompts, planners, or
   model logic. Integrations (Claude Code, harnesses) are thin adapters.
6. **Small surface.** A dozen well-chosen commands beat fifty. Every command
   answers a question the engineer would otherwise answer by hand.
7. **Human owns the merge.** AgentGrid informs, warns, and queues. It never
   merges, force-pushes, or rewrites history.

## 2. High-level architecture

```
+---------------------------------------------------------------+
|                         agentgrid CLI                         |
|  (cobra/urfave commands -> dispatch to use-case handlers)     |
+----------------+--------------------+-------------------------+
                 |                    |
                 v                    v
        +------------------+   +-------------------+
        |  Core (domain)   |   |   Policy engine   |
        | agents, claims,  |   | overlap, stale,   |
        | worktrees, diffs |   | diff-risk, PR     |
        +--------+---------+   +---------+---------+
                 |                       |
                 v                       v
        +------------------+    +-------------------+
        |   State (store)  |    |     Adapters      |
        |  SQLite via      |    | git, tmux, gh,    |
        |  sqlc/sqlx       |    | fs, optional      |
        +------------------+    | Claude Code shim  |
                                +-------------------+
```

The CLI is the only entrypoint in the MVP. Everything below it is a library
that could later be reused by a TUI, hook, or HTTP server without rewrites.

## 3. Module breakdown

### 3.1 `cmd/agentgrid` — CLI

- Command parsing (cobra recommended; small enough that urfave/cli is fine too).
- Output formatting: human, `--json`, `--quiet`.
- Global flags: `--repo`, `--db`, `--verbose`, `--no-color`.

### 3.2 `internal/core` — domain

Pure Go types and use cases. No SQL, no shelling out.

- `agent.go` — Agent struct + lifecycle states.
- `claim.go` — Claim struct, claim kinds (`path`, `module`, `glob`), intent
  (`edit`, `read`, `create`, `delete`).
- `worktree.go` — Worktree metadata.
- `diff.go` — Diff snapshot + risk vector.
- `event.go` — Append-only event types.

### 3.3 `internal/store` — state

- SQLite wrapper. One connection, WAL mode, busy_timeout set.
- Migrations embedded via `embed.FS`; applied on `init` and on every open.
- Query layer kept small and explicit (hand-written SQL or `sqlc`; no ORM).
- All writes go through the `Store` interface — the policy and CLI layers
  never see raw `*sql.DB`.

### 3.4 `internal/git` — git adapter

- Thin wrapper over `os/exec` calls to `git`.
- Functions: `RevParse`, `MergeBase`, `DiffNameOnly`, `DiffNumstat`,
  `WorktreeAdd`, `WorktreeRemove`, `BranchExists`, `IsClean`, `RemoteHead`.
- Returns typed results; never leaks raw stderr to the user except in
  `--verbose`.

### 3.5 `internal/tmux` — tmux adapter (optional)

- Functions: `HasSession`, `NewSession`, `KillSession`, `ListSessions`.
- MVP: read-only — record session name when given, verify it exists during
  `status`.

### 3.6 `internal/gh` — GitHub adapter

- Wraps `gh` CLI in MVP (assume installed and authenticated).
- Functions: `PRCreate`, `PRView`, `PRStatus`, `IssueView`.
- V1+ may swap to `go-github` if more control is needed.

### 3.7 `internal/policy` — rules engine

The single place where coordination decisions are made.

- `overlap.go` — claim overlap evaluation.
- `stale.go` — staleness derivation.
- `diffrisk.go` — diff-risk scoring.
- `forbidden.go` — forbidden-path matching.
- `pr.go` — PR readiness check.
- `status.go` — derived status bucket per agent.

Each function takes the relevant slice of state and returns a typed verdict
plus structured reasons. Policy is pure (no I/O), so it is trivially testable.

### 3.8 `internal/config` — configuration

- Loads `.agentgrid/config.yaml` (YAML chosen over TOML for nested structures
  like glob lists; can revisit).
- Schema versioned. Unknown keys → warning, not error.

### 3.9 `internal/log` — logging

- Structured logging via `log/slog`.
- File sink at `.agentgrid/agentgrid.log`, level controlled by `--verbose`.

## 4. Data flow

### 4.1 Registering an agent with a claim

```
user: agentgrid agent add --task "..." --claim "pkg/billing/**:edit"
        |
        v
CLI parse args -> core.Agent + core.Claim
        |
        v
policy.overlap.Check(claim, active_claims_from_store)
        |
   overlap?
    /     \
  yes     no
   |       |
   v       v
exit 1   store.WriteAgent + store.WriteClaim + store.WriteEvent
          |
          v
   (optional) git.WorktreeAdd + git.BranchCreate
          |
          v
   print agent id + next steps
```

### 4.2 Status

```
user: agentgrid status
        |
        v
store.ListAgents -> []Agent
store.ListActiveClaims, store.LatestDiffSnapshot per agent
        |
        v
policy.status.Derive(agent, claims, diff, stale_marks, pr) -> bucket
        |
        v
formatter prints table or JSON
```

### 4.3 Refresh (stale + diff-risk recompute)

```
user: agentgrid refresh
        |
        v
for each active agent:
  git.MergeBase(base_branch, agent.branch)
  git.DiffNameOnly(merge_base..base_branch)         -> base_changed_files
  git.DiffNumstat(merge_base..agent.branch)         -> agent_changes
  policy.stale.Check(agent.claims + agent_changes,
                     base_changed_files)            -> stale verdict
  policy.diffrisk.Score(agent_changes, claims,
                        forbidden, thresholds)      -> risk vector
  store.WriteDiffSnapshot + store.WriteStaleMark
  store.WriteEvent
```

## 5. State model (overview)

See `SCHEMA.md` for the full schema. The shape:

- `agents` is the spine. Everything joins to it.
- `claims` are time-bounded and have a status (`active`, `released`, `expired`).
- `worktrees` are 1:1 with agents in the MVP.
- `diff_snapshots` is append-only history; "current" is the most recent row.
- `stale_marks` are also append-only; "current" is the most recent unresolved
  row per agent.
- `events` is the audit log.
- `prs` records the handoff to GitHub.

Indexes are explicit (`agent_id`, `status`, `created_at`). No triggers, no
views in the MVP — projections are computed in Go.

## 6. CLI layer

- Cobra command tree mirrors the noun-verb shape (`agent add`, `claim list`).
- Every command:
  1. Loads config.
  2. Opens store (read-only or read-write as appropriate).
  3. Calls one or more use-case functions in `internal/usecase/`.
  4. Renders output via a formatter.
- `--json` is supported on every read command. Write commands return a
  machine-readable summary on `--json`.
- Exit codes:
  - `0` success.
  - `1` user error (bad args, conflicting claim, refused confirmation).
  - `2` system error (git failure, db locked, unreadable config).
  - `3` policy refusal (`add` blocked by overlap, `pr` blocked by HIGH risk
    unless `--force`).

## 7. State layer

- SQLite, single file, WAL mode, foreign keys on.
- One global `Store` opened per command invocation; closed on exit.
- Migrations folder embedded; the binary applies pending migrations on open
  with a single-row `schema_migrations` table.
- Concurrency: SQLite plus a `BEGIN IMMEDIATE` on writes is sufficient for the
  expected workload (single user, few writes per second peak).

## 8. Git layer

- Required: `git >= 2.20` (for `git worktree` semantics MVP relies on).
- All git invocations run with `-C <repo_root>` and `--no-pager`.
- Output parsing is restricted to porcelain / `-z` modes where available
  (no parsing of human-formatted output).
- Worktrees live at `<repo>/.agentgrid/worktrees/<agent-name>` by default;
  configurable.

## 9. Tmux / session layer

MVP: opt-in metadata only.

- `agentgrid agent add --tmux <session>` records the session name.
- `agentgrid status` calls `tmux has-session -t <name>` to mark sessions
  alive/dead.

V1: `agentgrid spawn` may create a session and a window automatically when
`--tmux` is passed.

## 10. Policy layer

- Configuration-driven thresholds, no hard-coded magic numbers.
- All policy functions take typed inputs and return typed verdicts with
  structured `Reason` lists (machine and human readable).
- Easy to fuzz and unit-test in isolation.

Example signatures:

```go
func CheckOverlap(new Claim, active []Claim) OverlapVerdict
func ScoreDiff(d DiffSnapshot, claims []Claim, cfg DiffRiskConfig) RiskVerdict
func DeriveStatus(a Agent, in Inputs) StatusBucket
```

## 11. GitHub handoff layer

- Reads diff snapshot + claims + risk + reviewer notes + tests run.
- Renders a markdown template (embedded; user-overridable in `.agentgrid/`).
- Calls `gh pr create --title ... --body @<file>` unless `--dry-run`.
- Records the PR url and number in the `prs` table.

## 12. Integration points (future-facing, not implemented in MVP)

- **Harnesses (Workmux, Claude Squad, jcode, Ruflo):** documented
  "claim protocol" — a small CLI surface (`agentgrid claim add --json`) and
  exit-code contract these tools call when they spawn or finish a session.
- **Claude Code:** thin shim that, given a registered agent, prints the
  recommended `claude` invocation (working dir, model, system prompt
  references). V1 may grow a `spawn --runner=claude` that runs it for the user.
- **Remote sandboxes (Depot et al.):** later, `worktree.kind = "remote"` with
  a runner adapter. Same claims, same policy.
- **GitHub Agent HQ / Copilot agents:** later, ingestion of remote-agent state
  alongside local agents, behind a feature flag.

## 13. Boundaries and non-goals

- No background daemon in MVP. If one is added (V1), it is opt-in
  (`agentgrid daemon`) and the CLI still works without it.
- No multi-machine sync. State is per-checkout.
- No write operations on other agents' branches or worktrees beyond cleanup
  with explicit confirmation.
- No prompt templates, no LLM calls, no embeddings.
- No web UI in MVP or V1.

## 14. Reliability and failure model

- Every command is idempotent where reasonable. Re-running `refresh` is safe.
- If `git` returns non-zero, the operation aborts; nothing partial is written.
- If `gh` is unavailable, `agentgrid pr` falls back to printing the rendered
  body and the suggested `gh` command.
- The state DB is recoverable: deleting `.agentgrid/state.db` only loses local
  metadata; branches, worktrees, and PRs remain.
