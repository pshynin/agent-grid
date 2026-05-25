# CLI_SPEC.md — AgentGrid

## Conventions

- All commands accept `--repo <path>` (default: git toplevel of `cwd`) and
  `--db <path>` (default: `<repo>/.agentgrid/state.db`).
- All read commands accept `--json` and `--quiet`.
- `<agent>` arguments accept an agent name or a unique id prefix.
- Exit codes (see `ARCHITECTURE.md` §6): `0` success, `1` user error,
  `2` system error, `3` policy refusal.
- Colors auto-detect TTY; `--no-color` disables.

---

## `agentgrid init`

Initialize AgentGrid in the current repo.

```
agentgrid init [--base-branch main] [--worktree-root .agentgrid/worktrees]
```

Effect:

- Creates `.agentgrid/` (if absent), writes `config.yaml` with defaults and
  comments, creates `state.db` and applies migrations.
- Records the repo root.
- Idempotent: re-running on an initialized repo prints status, does not
  overwrite config.

Example output:

```
$ agentgrid init
initialized .agentgrid/
  config:    .agentgrid/config.yaml
  database:  .agentgrid/state.db
  worktrees: .agentgrid/worktrees
  base:      main
next steps:
  agentgrid agent add --name <slug> --task "<short task>"
```

Errors:

- Not inside a git repo → exit `1` with hint to run `git init` first.
- `.agentgrid/state.db` exists but is unreadable → exit `2`.

---

## `agentgrid agent`

### `agent add`

```
agentgrid agent add
  --name <slug>
  --task "<short description>"
  [--role architect|slice|reviewer|qa|security|other]   (default: slice)
  [--runner claude|codex|gemini|cursor|manual]           (default: manual)
  [--base <branch>]                                      (default: config.default_base_branch)
  [--claim <kind:pattern:intent>] ...                    (repeatable)
  [--worktree <existing-path>]                           (adopt existing)
  [--tmux <session-name>]                                (optional)
```

Claim shorthand: `path:internal/auth/types.go:read`,
`glob:pkg/billing/**:edit`, `module:auth:edit`.

Effect:

- Validates that name is unique.
- Validates each claim and checks overlap; exits `3` on hard overlap.
- Writes the agent and any claims.
- Does **not** create a worktree (use `spawn` separately).
- Prints recommended next steps.

Example:

```
$ agentgrid agent add --name billing-extract --task "extract billing module" \
    --claim glob:pkg/billing/**:edit --claim glob:internal/invoice/**:read

agent added: billing-extract (01JCXY...)
claims:
  glob pkg/billing/**           edit
  glob internal/invoice/**      read

no overlapping active claims.
next: agentgrid spawn --from billing-extract
```

Overlap refusal example:

```
$ agentgrid agent add --name invoice-refactor --task "..." \
    --claim glob:internal/invoice/**:edit

error: claim conflicts with active claims of other agents
  conflict: billing-extract holds `glob internal/invoice/** read`
  intent edit on either side makes this a HARD conflict
hint:
  - narrow scope (e.g. internal/invoice/api/**)
  - coordinate with billing-extract owner
  - or release the conflicting claim first
exit: 3
```

### `agent list`

```
agentgrid agent list [--owner <user>] [--bucket <bucket>] [--json]
```

Columns: NAME, ROLE, STATUS, BRANCH, RISK, FLAGS, OWNER, AGE.

### `agent show`

```
agentgrid agent show <agent> [--json]
```

Full record: claims, worktree, last diff snapshot, current stale, last
events.

### `agent rm`

```
agentgrid agent rm <agent> [--with-worktree] [--with-branch] [--force]
```

Refuses if worktree is dirty, branch is unmerged, and `--force` is not
present. Records `agent.removed`. Use this rather than `git worktree remove`
when the worktree was created by AgentGrid.

---

## `agentgrid claim`

### `claim add`

```
agentgrid claim add <agent> <kind>:<pattern>:<intent> [<kind>:<pattern>:<intent> ...]
  [--risk low|normal|high] [--expires <duration>] [--notes "..."]
```

Behavior identical to `agent add --claim ...`, against an existing agent.
Hard overlap exits `3`; soft overlap warns and records an event.

### `claim list`

```
agentgrid claim list [--agent <agent>] [--active] [--json]
```

### `claim release`

```
agentgrid claim release <agent> [<pattern>...] [--all]
```

### `claim check`

```
agentgrid claim check <kind>:<pattern>:<intent>
```

Reports what would happen if this claim were added by a new agent, without
writing anything. Useful for scripting overlap probes.

```
$ agentgrid claim check glob:pkg/billing/**:edit
HARD conflict with billing-extract (claim: glob pkg/billing/** edit)
exit: 3
```

---

## `agentgrid spawn`

```
agentgrid spawn
  [--from <agent>]                       (use existing agent record)
  [--name <slug> --task "..." --claim ...] (create + spawn in one step)
  [--base <branch>]
  [--worktree <path>]                    (override default worktree path)
  [--branch <name>]                      (override default branch name)
  [--tmux <session>]                     (record session; do not create)
```

Effect:

- Resolves base commit.
- Creates a worktree at `worktrees.root/<agent-name>` (default).
- Creates a branch named `<prefix><agent-name>` (default; configurable).
- Records the worktree, sets `agents.status = working`, sets `base_commit`.
- Prints suggested next-step commands for the configured runner (e.g.,
  `cd <worktree> && claude`), but does **not** launch any agent process in
  the MVP.

Example:

```
$ agentgrid spawn --from billing-extract
worktree: .agentgrid/worktrees/billing-extract
branch:   billing-extract (from main @ 8a3f1c2)
status:   working

suggested:
  cd .agentgrid/worktrees/billing-extract && claude
```

---

## `agentgrid status`

```
agentgrid status [--bucket <b>[,<b>...]] [--owner <user>] [--risk <level>] [--json]
```

Default human output (color, width-aware):

```
$ agentgrid status

NAME              BUCKET           BRANCH                BASE   AHEAD  RISK   FLAGS         AGE
billing-extract   working          billing-extract       main   12     low    -             2h
invoice-refactor  stale            invoice-refactor      main   4      med    stale         5h
auth-cleanup      diff_too_large   auth-cleanup          main   38     high   no-tests      1d
docs-typos        ready_for_pr     docs-typos            main   2      low    -             10m
legacy-export     abandoned        legacy-export         main   0      -      -             7d

5 agents | 1 ready for PR | 1 stale | 1 diff_too_large
```

`--json` returns one object per agent with all derived fields.

---

## `agentgrid refresh`

```
agentgrid refresh [--agent <agent>] [--quiet]
```

Recomputes stale + diff-risk for one or all agents. Prints a one-line
summary per agent. Safe to run repeatedly.

---

## `agentgrid stale`

```
agentgrid stale [--json] [--agent <agent>] [resolve <agent> --reason "..."]
```

Default lists current open stale marks:

```
$ agentgrid stale

AGENT              REASON                                  RECOMMEND   FILES
invoice-refactor   base advanced into claim (3 files)      rebase      pkg/billing/types.go, ...
auth-cleanup       base advanced into modified set         re-plan     internal/auth/session.go, ...
```

`agentgrid stale resolve <agent> --reason "..."` records resolution.

---

## `agentgrid diff-risk`

```
agentgrid diff-risk <agent> [--json] [--no-refresh]
```

By default refreshes the snapshot first. Output:

```
$ agentgrid diff-risk auth-cleanup

agent:    auth-cleanup
branch:   auth-cleanup (38 ahead of main)
files:    37   lines: +1820 -612   modules: 5
level:    HIGH
reasons:
  files_over_high          37 > 30
  lines_over_medium        2432 > 600
  modules_over_high        5 >= 5
  no_tests_with_code       code changed in pkg/auth/** with no matching test
hint:
  - split into smaller PRs along module boundaries
  - or pass --force --reason "..." when you run `agentgrid pr`
```

`--json` returns the full structured snapshot.

---

## `agentgrid review`

```
agentgrid review [--json]
```

Sugar for `status --bucket ready_for_pr,diff_too_large,stale,blocked`.

---

## `agentgrid pr`

```
agentgrid pr <agent>
  [--open]                    (call `gh pr create`)
  [--dry-run]                 (print body only)
  [--force]                   (override HIGH risk or open stale)
  [--reason "..."]            (recorded with override)
  [--title "..."]             (override generated title)
  [--draft]
```

Default (without flags) renders the body, writes it to
`.agentgrid/prs/<agent>.md`, and prints both the body path and the
suggested `gh` command. With `--open`, runs `gh pr create` directly.

Rendered body sections (template default):

```
## Task
<short task>

## Claimed scope
- glob pkg/billing/**           edit
- glob internal/invoice/**      read

## Actual changes
- files: 14  lines: +420 -180  modules: billing, invoice

## Risk
LOW

## Tests
- has tests: yes (pkg/billing/billing_test.go, ...)

## Reviewer notes
<from latest review row, if any>

## Notes
<any --notes provided or empty>
```

Refusals:

- HIGH risk without `--force` → exit `3`, print reasons.
- Open stale without `--force` → exit `3`, print stale recommendation.
- Dirty worktree → exit `1`.

---

## `agentgrid cleanup`

```
agentgrid cleanup [--yes] [--include-branches] [--json]
```

Proposes safe removals (see `ALGORITHMS.md` §7). Without `--yes`, prompts
per item. With `--yes`, removes the proposed set in one batch and prints a
summary. `--include-branches` also deletes merged local branches.

Refuses to touch worktrees that AgentGrid did not create.

---

## `agentgrid help` / `agentgrid version`

Standard. `version` prints binary version, schema version, and config
schema version.

---

## Status output cheat sheet

Buckets:

| Bucket            | Meaning                                                |
| ----------------- | ------------------------------------------------------ |
| `registered`      | Created, no branch yet.                                |
| `working`         | Active branch, no blockers detected.                   |
| `blocked`         | User-marked or system-marked blocked.                  |
| `stale`           | Base advanced into claim or modified set.              |
| `diff_too_large`  | Risk = HIGH and no PR open.                            |
| `ready_for_pr`    | Clean, ahead > 0, no stale, risk < HIGH or overridden. |
| `pr_open`         | PR exists and is open (draft or normal).               |
| `merged`          | Agent or PR is merged.                                 |
| `abandoned`       | Explicitly abandoned.                                  |

Flags (composable, shown after risk):

- `stale` — current stale mark.
- `forbidden` — diff touches forbidden path.
- `no-tests` — code without tests heuristic.
- `dirty` — uncommitted changes in worktree.
- `tmux-dead` — recorded session no longer exists.
- `claim-violation` — modifications outside claimed scope.
