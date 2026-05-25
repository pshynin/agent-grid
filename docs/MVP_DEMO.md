# AgentGrid MVP — end-to-end demo

A copy-paste walkthrough that exercises everything the v0.1 MVP does:

- registers two agents against existing branches
- refuses an overlapping `edit` claim
- detects staleness when the base branch moves into a claimed scope
- scores a branch with `diff-risk`
- shows one `status` table across both agents

The demo runs entirely in a temporary git repository on your local
machine. It writes nothing outside that directory and makes no network
calls.

## 0. Prerequisites

- `git >= 2.20`
- The `agentgrid` binary on your `PATH`
  (`go install github.com/pshynin/agent-grid/cmd/agentgrid@latest`)

## 1. Create a throwaway repo

```sh
TMP=$(mktemp -d)
cd "$TMP"

git init -q -b main
mkdir -p pkg/billing pkg/auth
echo 'package billing' > pkg/billing/types.go
echo 'package auth'    > pkg/auth/session.go
echo '.agentgrid/'     > .gitignore
git add .
git -c user.email=you@example.com -c user.name=you commit -q -m "init"
```

You should have one commit on `main` with two source files and a
`.gitignore` that excludes the local AgentGrid state directory.

> **Important.** Always add `.agentgrid/` to your repo's `.gitignore`
> before committing. The SQLite state lives there; you do not want it
> tracked. AgentGrid does not write `.gitignore` for you in v0.1.

## 2. Initialize AgentGrid

```sh
agentgrid init
```

Expected:

```
initialized .agentgrid/
  repo:     /private/var/folders/.../tmp.XXXX
  config:   .agentgrid/config.yaml
  database: .agentgrid/state.db
  base:     main
```

`agentgrid init` is idempotent — re-running it is safe and will only
apply new migrations.

## 3. Create two branches (AgentGrid does not create branches for you)

```sh
git checkout -q -b feat/billing
echo '// extend billing' >> pkg/billing/types.go
git -c user.email=you@example.com -c user.name=you commit -q -am "billing edit"

git checkout -q -b feat/auth main
echo '// read-only review' >> pkg/auth/session.go
git -c user.email=you@example.com -c user.name=you commit -q -am "auth read"

git checkout -q main
```

`feat/billing` and `feat/auth` both branched off the initial commit and
each have one commit ahead of `main`.

## 4. Register two agents with claims

```sh
agentgrid agent add \
  --name billing \
  --task "extract billing module" \
  --branch feat/billing \
  --claim 'glob:pkg/billing/**:edit'

agentgrid agent add \
  --name auth \
  --task "audit auth read paths" \
  --branch feat/auth \
  --claim 'glob:pkg/auth/**:read'
```

> Quote claim specs whenever the pattern contains `*` or `**`. Otherwise
> zsh and similar shells try to glob-expand them against the local
> filesystem and the command fails with `no matches found` before
> `agentgrid` ever runs.

Expected (for each):

```
agent added: billing (01J...)
  branch:   feat/billing
  base:     main @ <sha>
  claims:
    glob  pkg/billing/**  edit
```

## 5. Watch overlap detection refuse a conflicting claim

```sh
agentgrid claim check 'glob:pkg/billing/sub/**:edit'
echo "exit=$?"
```

Expected (exit code `3`, the policy-refusal code):

```
error: claim glob:pkg/billing/sub/**:edit conflicts with existing claims:
  billing holds: glob pkg/billing/** edit
exit=3
```

`claim check` is a read-only probe — it writes nothing to the database.

A `read`-only claim against the same glob would succeed because `read+read`
is allowed:

```sh
agentgrid claim check 'glob:pkg/billing/**:read'
# -> "no conflicts."
```

## 6. Simulate `main` advancing into a claimed path

Suppose someone else's PR merges and lands a change in `pkg/billing/`.
We replay that locally:

```sh
echo '// changed on main' >> pkg/billing/types.go
git -c user.email=you@example.com -c user.name=you commit -q -am "main moves on"
```

`pkg/billing/types.go` has now been modified on `main` after the agents
branched off. That file is inside the `billing` agent's `edit` claim, so
`billing` should be marked stale on the next refresh.

## 7. Run `refresh` and read the stale list

```sh
agentgrid refresh
```

Expected:

```
refreshed 2 agents, 1 stale
  billing: stale (rebase) — 1 file
  auth: clean
```

```sh
agentgrid stale
```

Expected:

```
agent: billing
  branch:         feat/billing
  recommendation: rebase
  reason:         base advanced into claimed scope (1 file)
  files:
    pkg/billing/types.go
```

The recommendation depends on the claim intent:

- `edit` claim overlapped → `rebase`
- `read` claim overlapped → `review`
- both intents overlapped → `re-plan`

If `billing` rebases past the change (e.g.
`git checkout feat/billing && git merge --no-edit main`), the next
`agentgrid refresh` clears the mark automatically.

## 8. Score a branch with `diff-risk`

```sh
agentgrid diff-risk billing
```

Expected (a small in-scope diff scores LOW):

```
agent:    billing
branch:   feat/billing
head:     <sha>
files:    1
lines:    +1 -0
risk:     LOW
reasons:  (none)
```

Force a HIGH-risk verdict by touching a forbidden path and a file
outside the claim:

```sh
git checkout -q feat/billing
mkdir -p vendor
echo 'package vendor' > vendor/lib.go
echo 'package auth'   > pkg/auth/added.go
git add vendor/lib.go pkg/auth/added.go
git -c user.email=you@example.com -c user.name=you commit -q -m "wide diff"
git checkout -q main

agentgrid diff-risk billing
```

> Use a targeted `git add` here (not `git add .`). The `.agentgrid/`
> directory is gitignored in this demo, but more importantly the demo
> is showing you the shape of a real, mixed-scope diff.

Expected (vendor/ matches the configured forbidden glob, and both the
vendor file and `pkg/auth/added.go` are outside the billing agent's
claim — `forbidden_path_touched` and `claim_violation` are independent
checks, so a single file can show up in both lists):

```
agent:    billing
branch:   feat/billing
head:     <sha>
files:    3
lines:    +3 -0
risk:     HIGH
reasons:
  HIGH    forbidden_path_touched     1 forbidden path(s)
            vendor/lib.go
  MEDIUM  claim_violation            2 files modified outside claimed scope
            pkg/auth/added.go
            vendor/lib.go
```

`agentgrid diff-risk billing --no-refresh` re-reads the persisted snapshot
without recomputing — useful for scripting.

## 9. One table across all agents

```sh
agentgrid status
```

Expected (numbers will vary):

```
NAME      BRANCH         BASE   AHEAD/BEHIND   RISK   STALE   MERGED
billing   feat/billing   main   2/1            high   yes     no
auth      feat/auth      main   1/1            -      no      no
```

Columns:

- `AHEAD/BEHIND` — commits on the agent's branch vs. on the base branch
  since the live merge-base.
- `RISK` — the most recent persisted `diff-risk` level, or `-` if the
  command has not been run for this agent.
- `STALE` — `yes` if there is a current stale mark (clears on the next
  `refresh` after the conflict is resolved).
- `MERGED` — `yes` if the branch head is an ancestor of the base branch
  head (i.e. the branch has been merged in).

`agentgrid status --json` returns a stable-shaped array suitable for
piping into `jq`.

## 10. Reset

```sh
cd /
rm -rf "$TMP"
```

The whole demo lived under one temp directory and produced no state
outside it. Wiping `.agentgrid/state.db` in any AgentGrid-managed repo
also fully resets the local coordination state without touching `git`.

## What this demo proved

- Claim-before-touch: AgentGrid refuses an overlapping `edit` claim with
  a clear message and exit code `3`.
- Stale detection: when files inside a claim change on the base branch,
  the agent is marked stale with a structured recommendation.
- Diff-risk: a deterministic structural score based on configured
  thresholds and forbidden paths — no AI, no model calls, no PRs opened.
- Status: one table per agent that combines all three signals.
