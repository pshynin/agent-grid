# AgentGrid

> AgentGrid is a local control plane for coordinating multiple coding agents
> across branches by tracking claims, stale work, and diff risk.

It is a single Go binary plus a SQLite file under `.agentgrid/`. It runs
next to `git`. It does not launch coding agents, does not manage worktrees
for you, does not call any AI model, does not open PRs, and does not run a
dashboard.

## Problem

One coding agent in one terminal is easy. Five agents across branches,
worktrees, terminals, and PRs becomes coordination chaos:

- Two agents silently edit the same file.
- Agent B planned to refactor a module that Agent A already changed —
  B's plan is stale before its first commit.
- An agent grows a 4,000-line diff no one can review.
- An agent touches `vendor/` or `migrations/` without anyone noticing
  until CI.

These are coordination problems, not model problems. AgentGrid is the boring
layer that catches them.

The MVP value sentence:

> *"Agent B is stale because main changed `src/auth/session.ts` after Agent
> B claimed/read it."*

## What AgentGrid does today

The v0.1 MVP coordination engine:

- Registers coding-agent tasks against existing branches.
- Records path and glob claims (`edit` or `read`) before work starts.
- Detects claim overlap and refuses conflicting claims (`read+read` is
  allowed; any pairing involving `edit` is a hard conflict).
- Detects stale agents when files they claimed have changed on the base
  branch since they branched off, and recommends `rebase`, `review`, or
  `re-plan`.
- Scores branch diffs against configurable thresholds and forbidden
  paths, returning `low`, `medium`, or `high` with structured reason
  codes.
- Shows one status table across all agents with branch, ahead/behind,
  latest risk level, stale flag, and merged flag.
- Stable `--json` output on every read command for scripting.

## What AgentGrid does not do yet

Explicitly out of scope for v0.1:

- Does **not** launch Claude (or any other coding agent).
- Does **not** create or manage git worktrees.
- Does **not** create, comment on, or merge PRs.
- Does **not** run or attach to tmux sessions.
- Does **not** call AI models or use embeddings.
- Does **not** run a dashboard, web UI, or server.
- Does **not** auto-merge, auto-rebase, or auto-resolve anything.
- Does **not** sync state across machines.

These are tracked as future layers in `ROADMAP.md`. None of them are
prerequisites for the value the MVP provides.

## Quickstart

Requires `git >= 2.20`. Run inside an existing git repository.

```sh
# 1. Install
go install github.com/pshynin/agent-grid/cmd/agentgrid@latest

# 2. Initialize AgentGrid in your repo
agentgrid init

# 3. Register an agent against a branch you already created
git checkout -b feat/billing
git checkout main
agentgrid agent add \
  --name billing \
  --task "extract billing module" \
  --branch feat/billing \
  --claim glob:pkg/billing/**:edit \
  --claim glob:internal/invoice/**:read

# 4. Probe before committing to a new claim (writes nothing)
agentgrid claim check glob:pkg/billing/sub/**:edit

# 5. After other branches have moved on, recompute stale state
agentgrid refresh
agentgrid stale

# 6. Score the agent's branch against thresholds and forbidden paths
agentgrid diff-risk billing

# 7. One table across all agents
agentgrid status
```

A copy-paste end-to-end walkthrough lives in
[`docs/MVP_DEMO.md`](docs/MVP_DEMO.md).

## Exit codes

| Code | Meaning           | Examples                                              |
| ---- | ----------------- | ----------------------------------------------------- |
| `0`  | ok                | command succeeded                                     |
| `1`  | user error        | unknown agent, missing branch, malformed claim spec   |
| `2`  | system error      | (reserved; not used in v0.1)                          |
| `3`  | policy refusal    | overlapping `edit` claim, `claim check` finds conflict |

All commands are deterministic and idempotent where it makes sense.
`refresh` is safe to re-run; `claim check` and `--no-refresh` modes write
nothing.

## Why not just use Claude Code worktrees, Workmux, or Claude Squad?

Those tools help you **run** coding-agent sessions. AgentGrid tracks the
**coordination risk around those sessions**:

- Did anyone else already claim the path you're about to touch?
- Did the base branch move under your feet while you were working?
- Did your branch grow too large, drift outside your declared scope, or
  touch a forbidden path?

AgentGrid composes with whatever launcher and terminal multiplexer you use.
It is not a launcher and not a multiplexer. There is one source of truth
(the SQLite file under `.agentgrid/`) and you keep using `git` directly.

## Relationship to `agent-playbook`

- [`agent-playbook`](https://github.com/pshynin/agent-playbook) defines the
  **methodology**: agent roles, feature brief templates, vertical-slice
  workflow, small-PR discipline, review checklists.
- AgentGrid is the **runtime** that enforces a slice of that methodology:
  declare your scope before you start, and AgentGrid will refuse to let two
  agents declare the same scope.

## Core concepts

1. **Agent.** A name, a task, a branch, a base branch, and a captured
   base commit. AgentGrid does not own the branch — `git` does. AgentGrid
   only records the coordination metadata.
2. **Claim.** A pattern (`path` or `glob`) plus an intent (`edit` or
   `read`) that an agent declares before starting work. `read+read`
   overlap is allowed; any pairing involving `edit` is a hard conflict.
3. **Stale mark.** Set by `refresh` when files on the base branch
   changed inside any of the agent's claimed scope since the live
   merge-base of `branch` and `base_branch`. Rebasing past the change
   clears the mark on the next `refresh`.
4. **Diff-risk snapshot.** Computed by `diff-risk`. Files, lines,
   forbidden-path hits, and out-of-claim files combine into a
   `low | medium | high` level with structured reason codes. Persisted to
   SQLite so `status` and `--no-refresh` can read it without touching git.
5. **Status row.** A derived view: name, branch, base, ahead/behind,
   latest risk level (or `-`), stale flag, merged flag.

## Configuration

`agentgrid init` writes `.agentgrid/config.yaml` with documented
defaults:

```yaml
version: 1
default_base_branch: main

forbidden_paths:
  - vendor/**
  - node_modules/**
  - migrations/**
  - infra/prod/**

test_file_globs:
  - "**/*_test.go"
  - "**/test/**"
  - "**/__tests__/**"

diff_risk:
  thresholds:
    files_low: 5
    files_medium: 15
    files_high: 30
    lines_low: 200
    lines_medium: 600
    lines_high: 1500
```

The config is validated on every command; misconfigured values give a
single clear error and exit `1`.

## Design principles

- **Local first.** One Go binary, one SQLite file under `.agentgrid/`.
  No daemon, no server, no network calls.
- **Shell out, don't reimplement.** `git` does git; AgentGrid orchestrates.
- **Pull-based.** Run `refresh` when you want fresh state. Watchers are
  out of scope.
- **Policy is pure.** All coordination rules live in `internal/policy`
  as deterministic, table-tested functions.
- **Human owns the decision.** AgentGrid informs, warns, and refuses.
  It never merges, rebases, or rewrites history.

## Status

**MVP coordination engine implemented.**
v0.1 is shippable as a `go install` binary. The eight commands listed in
the quickstart all work end-to-end.

Future layers (`spawn`, tmux integration, GitHub/PR handoff, review queue
buckets, dashboards, multi-machine sync) are **not** part of v0.1. They
are described in [`ROADMAP.md`](ROADMAP.md) and may or may not be built
depending on whether the MVP loop proves valuable in practice.

> Note on CLI name: `agentgrid` is the primary command. A shorter `ag`
> alias may be added later, but it is not implemented and should not be
> used in scripts.

## Design documents

The product and engineering documents that drove the MVP:

- [`PRODUCT.md`](PRODUCT.md) — vision, target users, non-goals (forward-looking).
- [`MVP.md`](MVP.md) — locked v0.1 scope.
- [`ARCHITECTURE.md`](ARCHITECTURE.md) — modules, data flow, boundaries.
- [`ROADMAP.md`](ROADMAP.md) — phases 0–9 and cut lines.
- [`IMPLEMENTATION_PLAN.md`](IMPLEMENTATION_PLAN.md) — Go layout and task order.
- [`SCHEMA.md`](SCHEMA.md) — SQLite schema.
- [`ALGORITHMS.md`](ALGORITHMS.md) — overlap, stale, diff-risk, status.
- [`CLI_SPEC.md`](CLI_SPEC.md) — full command surface (some flags are
  forward-looking; see help text for what ships in v0.1).

## License

To be decided before the first tagged release (likely MIT or Apache-2.0).
