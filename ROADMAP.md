# ROADMAP.md — AgentGrid

Each phase ends with a usable, shippable state. No phase depends on the next.
Phases 0–6 form the MVP. Phases 7–9 are V1+ and optional.

A phase is "done" when:

- the listed deliverables work end-to-end,
- there are unit tests for the policy functions touched,
- there is a short README section showing the new commands in use,
- the binary still builds, installs, and runs on macOS and Linux from a clean
  checkout.

---

## Phase 0 — Design and skeleton

**Goal:** Repository is ready for implementation; design is locked enough to
build.

Deliverables:

- This document set: `PRODUCT.md`, `ARCHITECTURE.md`, `ROADMAP.md`,
  `IMPLEMENTATION_PLAN.md`, `SCHEMA.md`, `ALGORITHMS.md`, `CLI_SPEC.md`,
  `README.md`.
- Empty Go module: `go mod init`, license, basic Makefile, `golangci-lint`
  config, `goreleaser` skeleton.
- CI: build + vet + test on push (GitHub Actions, one workflow file).
- ADR template under `docs/adr/` for future architectural decisions.

Exit criteria: clean `go build` produces an `agentgrid` binary that prints
`agentgrid: no command — try 'agentgrid help'`.

---

## Phase 1 — Local state and claims

**Goal:** Engineer can register agents and claims, and the tool detects
overlap before work starts. No git yet.

Deliverables:

- `agentgrid init` writes `.agentgrid/config.yaml` and `state.db`; runs
  migrations.
- `agentgrid agent add|list|show|rm`.
- `agentgrid claim add|list|release|check`.
- Overlap detection on `claim add` and on `agent add --claim`.
- `agentgrid status` (minimal: just lists agents and their claims).
- `--json` output on all read commands.
- Unit tests for the overlap algorithm with a representative fixture set.

Exit criteria: scripted scenario in `docs/demos/phase1.md` shows two agents
being added, the second one blocked by overlap, then resolved by re-scoping.

---

## Phase 2 — Git / worktree integration

**Goal:** AgentGrid owns the link between agent and worktree/branch.

Deliverables:

- `agentgrid spawn` creates a worktree under `.agentgrid/worktrees/<name>`
  and a branch off `--base`.
- `agent add --worktree existing/path` adopts an existing worktree.
- `agentgrid agent rm` optionally removes worktree+branch with confirmation.
- `internal/git` package with `RevParse`, `MergeBase`, `WorktreeAdd`,
  `WorktreeRemove`, `BranchExists`, `IsClean`.
- `status` shows branch, base, ahead/behind, dirty/clean.
- Tests run against a temp repo seeded with `git init` + fixture commits.

Exit criteria: the demo from Phase 1 now ends with a real worktree on disk
and a branch pointing at the right base.

---

## Phase 3 — Stale detection

**Goal:** When another agent's commits invalidate an agent's plan, that agent
is marked stale with a clear reason.

Deliverables:

- `agentgrid refresh` recomputes staleness for all active agents.
- Stale logic compares the merge-base-since-base-changed file set against
  each agent's claim patterns and already-modified files.
- `stale_marks` table populated; `agentgrid stale` lists current marks.
- `status` shows a `STALE` flag with a one-line reason.
- Remedies surfaced: `rebase`, `re-plan`, `review-diff`, `narrow-scope`,
  `abandon`.

Exit criteria: scenario where Agent A merges and Agent C is automatically
marked stale on the next `refresh`, with the conflicting files listed.

---

## Phase 4 — Diff-risk scoring

**Goal:** A branch's diff is scored against thresholds and forbidden paths
before it becomes a PR.

Deliverables:

- `agentgrid diff-risk <agent>` reports a risk level (low/medium/high) and a
  list of structured reasons.
- Thresholds in `config.yaml` (files, lines, modules, forbidden paths,
  test-file ratio).
- Forbidden-path globs configurable per repo.
- Detection of "diff outside claims" (claim violation).
- Diff snapshots persisted per refresh.

Exit criteria: a known-large branch in a test repo scores HIGH with the
expected reasons; a known-tight branch scores LOW.

---

## Phase 5 — Status / review queue

**Goal:** `agentgrid status` is the single command an engineer runs to know
what to do next.

Deliverables:

- Status buckets derived per `ALGORITHMS.md` (working, blocked, stale,
  diff-too-large, ready-for-review, ready-for-PR, merged, abandoned).
- `agentgrid review` filters to "ready for review" + "stale" + "blocked".
- Sort and filter flags (`--bucket`, `--owner`, `--risk`).
- Color and width-aware table rendering for narrow terminals.

Exit criteria: status output is readable at 80 columns and informative at
160 columns; bucket assignment is unit-tested.

---

## Phase 6 — PR summary / GitHub handoff

**Goal:** Opening a PR from an agent's branch is one command and produces a
useful body.

Deliverables:

- `agentgrid pr <agent>` renders a markdown body with: task, claimed paths,
  actually touched paths, files/lines, modules, risk verdict + reasons,
  tests-run (if recorded), reviewer notes (if attached).
- `--open` invokes `gh pr create`.
- `--dry-run` prints the body without invoking `gh`.
- Template lives at `.agentgrid/templates/pr.md.tmpl` (overridable).
- HIGH risk requires `--force` or a `--reason "..."` flag.
- `prs` table records number, url, status.

Exit criteria: end-to-end demo: spawn → claim → work → refresh → pr →
PR opened on GitHub with the rendered body.

---

## Phase 7 — Tmux integration (optional)

Deliverables:

- `agent add --tmux <session>` records session metadata.
- `status` calls `tmux has-session` and shows alive/dead.
- `agentgrid spawn --tmux` creates the session and a window in the worktree
  directory, but does not auto-launch any specific runner.
- `agentgrid attach <agent>` runs `tmux attach -t <session>`.

---

## Phase 8 — TUI (optional)

Deliverables:

- Read-mostly Bubble Tea TUI:
  - Top: status table.
  - Bottom: detail pane for the selected agent (claims, diff stats, stale,
    risk, last events).
- Keybindings to run `refresh`, jump to worktree (`$EDITOR`), attach tmux,
  open PR.
- No writes that aren't also available on the CLI.

---

## Phase 9 — Remote / dashboard (optional, exploratory)

Deliverables:

- Read-only HTTP endpoint behind `agentgrid serve --bind 127.0.0.1:PORT`.
- JSON of current state + recent events.
- Static single-page view to render the same buckets as the CLI.
- Explicitly not multi-tenant. Explicitly not the source of truth.

This phase is exploratory and may be cut if the CLI/TUI cover real workflows.

---

## Cross-cutting tracks (run alongside phases)

- **Docs:** every phase updates `README.md` and `CLI_SPEC.md`.
- **Telemetry:** none in the binary by default. If added later, opt-in,
  local-only, no network.
- **Distribution:** `goreleaser` from Phase 0; Homebrew tap from Phase 6;
  binaries published from Phase 1.
- **Schema migrations:** every schema change ships with a forward-only
  migration; backwards compatibility maintained within MVP.

## Cut lines

If time is short, here is the order in which phases can be deferred without
killing the value proposition:

1. Phase 9 (cut first).
2. Phase 8.
3. Phase 7.
4. Phase 6 — degrade to PR body printed to stdout (no `gh` call).
5. Phase 5 — degrade to a flat list instead of buckets.

Phases 0–4 are the irreducible core. If those work, AgentGrid is already
useful.
