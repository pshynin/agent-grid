# PRODUCT.md — AgentGrid

## 1. Vision

A local control plane that lets one engineer safely run many coding agents in
parallel without the work collapsing into one giant unreviewable PR.

AgentGrid is **boring infrastructure for the agent era**: a single Go binary that
sits next to `git`, `tmux`, `gh`, and Claude Code and answers the questions a
human can no longer track by hand once three or more agents are working at once.

> "git status for a small army of coding agents,
> plus guardrails that stop agents from stepping on each other before PR chaos."

## 2. Target users

Primary:

- Individual senior engineers who already run 2–10 coding agents in parallel
  (Claude Code, Codex CLI, Cursor CLI, Gemini CLI, remote sandboxes).
- Engineers using git worktrees + tmux + GitHub PRs as their default loop.
- Engineers who follow a vertical-slice / small-PR discipline
  (e.g. [`agent-playbook`](https://github.com/pshynin/agent-playbook) users)
  and need a runtime that enforces it.

Secondary (later phases):

- Small teams sharing a repo where multiple engineers each run multiple agents.
- Authors of agent harnesses (Workmux, Claude Squad, jcode, Ruflo) who want a
  coordination layer to plug into rather than rebuild.

Out of scope as users:

- Teams that want a hosted SaaS dashboard as their primary surface.
- Teams that want one perfect autonomous agent rather than coordinating many.

## 3. Problem statement

One agent in one terminal is easy. Five agents across branches, worktrees, PRs,
terminals, and machines is a systems problem:

- Two agents silently edit the same file and produce conflicting branches.
- Agent B planned to refactor `pkg/auth` while Agent A is already rewriting it —
  B's plan is stale before its first commit.
- An agent produces a 4,000-line "while I was here" diff that no human can review.
- An agent touches `migrations/`, `infra/prod/`, or `vendor/` without anyone
  noticing until CI.
- Three branches are technically ready for review but no one remembers which.
- A worktree sits abandoned for a week; the branch is gone but the directory
  isn't.

These are coordination failures, not model failures. No better model fixes them.

## 4. Why this exists despite the landscape

| Tool / Category                       | What it does well                                  | What it does **not** address                         |
| ------------------------------------- | -------------------------------------------------- | ---------------------------------------------------- |
| Claude Code (native worktrees)        | Spawn a session in a worktree.                     | No cross-session awareness. No claims. No staleness. |
| Workmux / Claude Squad / Kage / Loom  | Manage tmux + worktree + agent session ergonomics. | No registered intent. No overlap or staleness.       |
| jcode / Ruflo                         | Broader harness, swarms, memory, orchestration.    | Framework-shaped; not a thin coordination layer.     |
| Depot Remote Agents / cloud sandboxes | Move execution off the laptop.                     | Doesn't answer "who owns what" across N agents.      |
| GitHub Agent HQ / Copilot agents      | Push agents into issues, PRs, CI.                  | Post-PR; doesn't prevent pre-PR collisions.          |

The unoccupied seat is **pre-PR operational coordination on the engineer's own
machine**. AgentGrid takes that seat and stays small.

It is *with* these tools, not *against* them. Workmux can spawn the session;
AgentGrid records the claim. Claude Code can run the agent; AgentGrid notices
when the agent went stale. `gh` can open the PR; AgentGrid writes the summary.

## 5. Core use cases

1. **Plan a slice, register intent.** Engineer creates Agent A for
   "extract billing module", with claimed paths `pkg/billing/**`,
   `internal/invoice/**`. AgentGrid creates the worktree + branch and records
   the claim.
2. **Detect overlap before work starts.** Engineer tries to register Agent B
   for "refactor invoice service" claiming `internal/invoice/**`. AgentGrid
   blocks: Agent A already holds an `edit` claim there. Engineer either waits,
   re-scopes, or coordinates explicitly.
3. **Notice staleness.** Agent A merges. Agent C, working on a different slice,
   had `pkg/billing/types.go` in its read set. AgentGrid marks Agent C stale
   with reason "Agent A touched 3 files in your read set since your base
   commit; rebase recommended."
4. **Catch oversized diffs.** Agent D has changed 47 files / 2,300 lines across
   6 modules. `agentgrid diff-risk D` returns **HIGH** with a list of reasons.
   Engineer splits the work before opening a PR.
5. **Triage the review queue.** `agentgrid status` shows: 2 working, 1 blocked,
   1 stale, 2 ready for review, 1 abandoned. Engineer knows where to spend
   the next 20 minutes.
6. **Hand off cleanly.** `agentgrid pr D` opens a PR via `gh` with task name,
   claimed vs. actual touched paths, diff stats, risk summary, tests run, and
   a link to the issue.
7. **Clean up.** `agentgrid cleanup` proposes which worktrees and branches are
   safe to remove (merged or explicitly abandoned, no uncommitted changes) and
   asks before deleting.

## 6. Non-goals

AgentGrid will **not**:

- Implement a new agent framework, planner, or scheduler.
- Be a chat UI or an "agent IDE".
- Auto-merge, auto-rebase, or auto-resolve conflicts.
- Run agents remotely in the MVP (no cloud sandbox, no server).
- Replace Claude Code, Workmux, Claude Squad, jcode, Ruflo, or `gh`.
- Provide a multi-machine sync or team dashboard in the MVP.
- Hide `git` or `tmux` — both remain the user's primary tools.
- Embed prompts or model-specific logic. AgentGrid is model-agnostic at the
  coordination layer; integrations are thin shells.

## 7. MVP scope (single engineer, single repo, local)

In:

- `agentgrid init` — create `.agentgrid/` with `config.yaml` and `state.db`.
- Agent registry: add, list, show, remove agents.
- Claim registry: add, list, release; overlap check on add.
- Optional worktree provisioning (`spawn`) that shells out to `git worktree`.
- Status view across all agents.
- Stale detection driven by an explicit `agentgrid refresh` (no daemon).
- Diff-risk scoring against configurable thresholds and forbidden paths.
- PR summary generation; `--open` shells out to `gh`.
- Cleanup of merged / abandoned worktrees with confirmation.

Out (deferred to V1+):

- Tmux session bookkeeping (read-only at first).
- Reviewer-agent and QA-agent integration.
- TUI.
- Multi-repo and multi-machine support.
- Background daemon and file-watching.
- Web dashboard.

## 8. V1 scope

After MVP proves the loop, V1 adds:

- Tmux integration: associate a session/window with an agent; `attach` command.
- Lightweight watcher (opt-in `agentgrid daemon`) that updates staleness and
  diff-risk on commit/checkout events instead of on `refresh`.
- Hooks: pre-commit / post-commit hooks that auto-update agent diff snapshots.
- Reviewer/QA agent output ingestion (read JSON or markdown from a known path
  and attach it to the agent record).
- Richer PR template with playbook-aligned sections.
- `agentgrid claim suggest` — propose claim patterns by reading the agent's
  task brief.
- Export/import of state for sharing between machines.

## 9. Future roadmap (post-V1, optional)

- Read-only TUI (Bubble Tea) for `status`, `stale`, `review`.
- Multi-repo workspaces.
- Plugin protocol for harnesses (Workmux, Claude Squad, jcode) to register
  agents and claims programmatically.
- Optional thin server for team-shared views (single-tenant, read-mostly).
- Integration with Depot or other remote sandboxes as just another agent kind.
- GitHub Agent HQ bridge: pull agent state from GitHub, surface it next to
  local agents.

Each later phase is opt-in. The CLI stays the source of truth.

## 10. Success criteria

MVP is successful if:

- A single engineer running 3+ Claude Code agents reports that AgentGrid
  prevented at least one overlap, stale rework, or oversized PR per week.
- `agentgrid status` becomes a 5-second answer to "what is each of my agents
  doing right now?"
- The tool installs as a single binary, requires no daemon, and adds < 50 ms
  per command on a warm SQLite.
- The codebase is small enough that one engineer maintains it without
  abstractions they regret in three months.

V1 is successful if:

- At least one third-party harness (Workmux/Claude Squad/jcode/Ruflo) integrates
  via the documented claim protocol.
- Diff-risk and stale signals are trusted enough that engineers act on them
  without re-deriving by hand.

## 11. Relationship to `agent-playbook`

- [`agent-playbook`](https://github.com/pshynin/agent-playbook) defines
  **methodology**: agent roles (architect, slice worker, reviewer, QA,
  security), feature brief templates, vertical-slice workflow, small-PR
  discipline, review checklists, CLAUDE.md templates.
- AgentGrid is the **runtime** that holds engineers and agents to that
  methodology: it captures the intent the playbook tells you to declare,
  enforces the small-PR discipline the playbook recommends, and produces the
  PR summary the playbook prescribes.

If the playbook says "declare your scope before you start," AgentGrid is the
thing that refuses to let two agents declare the same scope.
