# AgentGrid

> A local control plane for running many coding agents in parallel — without
> turning your repo into one giant unreviewable PR.

AgentGrid is a single-binary CLI that sits next to `git`, `tmux`, `gh`, and
Claude Code. It tracks which agent owns which task, which files each agent
plans to touch, who is stepping on whom, whose branch went stale, and which
diff has grown too large to review.

It is **not** another agent launcher. It is the missing **coordination layer**
that becomes necessary the moment you run more than one coding agent at a
time.

---

## Why this exists

One Claude Code session in one terminal is easy.

Five agents across branches, worktrees, PRs, terminals, and machines is a
systems problem:

- Two agents silently edit the same file and produce conflicting branches.
- Agent B planned to refactor a module that Agent A is already rewriting —
  B's plan is stale before its first commit.
- An agent produces a 4,000-line diff that no one can review.
- An agent touches `migrations/` or `infra/prod/` without anyone noticing
  until CI.
- Three branches are technically ready for review but no one remembers
  which.

These are coordination failures, not model failures. AgentGrid is the boring
layer that catches them.

## How it relates to other tools

| Tool                                  | Role                                          | AgentGrid's role               |
| ------------------------------------- | --------------------------------------------- | ------------------------------ |
| Claude Code (and worktrees)           | Run an agent in a session.                    | Track *what* the agent owns.   |
| Workmux / Claude Squad / Kage / Loom  | Manage tmux + worktree ergonomics.            | Manage *intent and conflict*.  |
| jcode / Ruflo                         | Broader harness, swarms.                      | Stay narrow: coordination.     |
| Depot Remote Agents                   | Move execution off the laptop.                | Local source of truth.         |
| GitHub Agent HQ / Copilot agents      | Push agents into PRs and CI (post-PR).        | Pre-PR coordination.           |

AgentGrid does not replace any of these. It composes with them.

## Relationship to `ai-agent-playbook`

- [`ai-agent-playbook`](#) defines **methodology** — agent roles
  (architect, slice worker, reviewer, QA, security), feature brief templates,
  vertical-slice workflow, small-PR discipline, review checklists.
- **AgentGrid is the runtime** that enforces that methodology: it captures the
  intent the playbook tells you to declare, refuses to let two agents declare
  the same scope, surfaces stale work, and pushes back on oversized PRs.

If the playbook says "declare your scope before you start," AgentGrid is the
thing that refuses to let two agents declare the same scope.

## Status

**Phase 0 — design.** No implementation yet. The design lives in:

- [`PRODUCT.md`](PRODUCT.md) — vision, users, scope, non-goals.
- [`ARCHITECTURE.md`](ARCHITECTURE.md) — modules, data flow, boundaries.
- [`ROADMAP.md`](ROADMAP.md) — phases 0–9, MVP cut lines.
- [`IMPLEMENTATION_PLAN.md`](IMPLEMENTATION_PLAN.md) — Go layout, first 10
  tasks.
- [`SCHEMA.md`](SCHEMA.md) — SQLite schema.
- [`ALGORITHMS.md`](ALGORITHMS.md) — overlap, stale, diff-risk, status,
  cleanup.
- [`CLI_SPEC.md`](CLI_SPEC.md) — every command, every flag, every output.

## Quick start (planned)

> The commands below describe the MVP target. The binary doesn't exist yet.

```sh
# 1. Install (once published)
brew install <user>/agentgrid/agentgrid

# 2. Inside a git repo
agentgrid init

# 3. Register an agent and its intended scope
agentgrid agent add \
  --name billing-extract \
  --task "extract billing module" \
  --claim glob:pkg/billing/**:edit \
  --claim glob:internal/invoice/**:read

# 4. Create a worktree + branch for that agent
agentgrid spawn --from billing-extract

# 5. Open Claude Code (or your runner) in the worktree
cd .agentgrid/worktrees/billing-extract && claude

# 6. From any other terminal, see everything at once
agentgrid status

# 7. After commits, refresh state and check risk
agentgrid refresh
agentgrid diff-risk billing-extract

# 8. When ready, hand off to GitHub
agentgrid pr billing-extract --open
```

## Core concepts

1. **Agent registry.** Every agent session has a name, role, task, branch,
   worktree, owner, status.
2. **Claim before touch.** Before an agent edits anything, it registers its
   intent (paths/modules + edit/read). Overlapping `edit` claims are blocked.
3. **Stale detection.** When another agent's commits land in your base
   branch and they touch files you claimed or already modified, your agent
   is marked stale with a recommendation (rebase, re-plan, narrow, abandon).
4. **Diff-risk scoring.** Files / lines / modules / forbidden paths /
   missing tests / claim violations → `low | medium | high`. HIGH blocks PR
   creation without an explicit override.
5. **Review queue.** `agentgrid status` groups every agent into a bucket:
   `working`, `blocked`, `stale`, `diff_too_large`, `ready_for_pr`,
   `pr_open`, `merged`, `abandoned`.
6. **GitHub handoff.** `agentgrid pr <agent>` renders a structured PR body
   (task, claimed scope, actual changes, risk, tests, reviewer notes) and
   shells out to `gh`.
7. **Human in control.** AgentGrid never auto-merges, never force-pushes,
   never rewrites history. It informs, warns, and queues.

## Example workflow

1. `agentgrid init` inside the repo.
2. Engineer drafts a slice for billing extraction.
3. `agentgrid agent add ... --claim glob:pkg/billing/**:edit`.
4. AgentGrid checks for overlap — none. Records the agent.
5. `agentgrid spawn` creates `pkg/.agentgrid/worktrees/billing-extract` and
   a branch off `main`.
6. Engineer opens Claude Code in that worktree.
7. A second agent is added for invoice work. Its claim overlaps the first.
   AgentGrid **blocks** the add until the scope is narrowed.
8. The first agent merges. The third agent — which had a read claim on
   `pkg/billing/types.go` — is marked **stale** on the next `refresh`.
9. The third agent grows to 47 files / 2,300 lines. `diff-risk` reports
   **HIGH**. The engineer splits the work before opening a PR.
10. `agentgrid pr` opens a PR with a structured body. Human reviews and
    merges.
11. `agentgrid cleanup` proposes safe removal of the merged worktree and
    branch.

## Non-goals

AgentGrid will not:

- Implement a new agent framework, planner, or scheduler.
- Be a chat UI or an "agent IDE."
- Auto-merge, auto-rebase, or auto-resolve conflicts.
- Run agents remotely (no cloud sandbox in MVP).
- Replace Claude Code, Workmux, Claude Squad, jcode, Ruflo, or `gh`.
- Provide a multi-machine or team dashboard in the MVP.
- Hide `git` or `tmux` — both remain the user's primary tools.

If something feels like Airflow for coding agents, it's the wrong direction.

## Design principles

- **Local first.** One Go binary, one SQLite file under `.agentgrid/`. No
  daemon, no server in the MVP.
- **Shell out, don't reimplement.** `git`, `tmux`, `gh` are the right tools;
  we orchestrate them.
- **Pull-based.** You run `refresh` when you want fresh state. Watchers are
  optional and later.
- **Policy is pure.** All coordination rules are pure functions, table-tested.
- **Small surface.** A dozen commands beat fifty.

## License

To be decided before the first release (likely MIT or Apache-2.0).

## Contributing

This project is in Phase 0 (design). Issues and design feedback are welcome;
implementation contributions wait until after the first release branch is
cut.
