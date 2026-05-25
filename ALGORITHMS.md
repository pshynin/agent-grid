# ALGORITHMS.md — AgentGrid

All algorithms are deterministic, pure where possible, and operate on
structured input. They are implemented in `internal/policy/` and tested with
table-driven cases.

Notation:

- `A`, `B`, `C` — agents.
- `P(x)` — pattern set of agent `x` (its active claims expanded to globs).
- `M(x)` — set of files agent `x` has actually modified in its branch.
- `B_x` — base commit at which agent `x` started.
- `base_branch` — the agent's configured base.

---

## 1. Claim overlap detection

**Inputs**

- `new_claim`: kind, pattern, intent.
- `active_claims`: list of currently active claims (`agent_id`, kind, pattern,
  intent).
- `modules_map`: config map from glob → module name.

**Outputs**

- `verdict`: `none | soft | hard`.
- `reasons`: list of `{ other_agent_id, other_pattern, overlap_kind }`.

**Procedure**

1. Resolve `new_claim` and each `active_claim` to a canonical glob form:
   - `path "p"` → glob `"p"` (literal).
   - `module "m"` → globs from `modules_map` reverse lookup.
   - `glob "g"` → `"g"`.
2. For each active claim `c`:
   1. If `c.agent_id == new_claim.agent_id`, skip (same agent may layer
      claims).
   2. Compute `globs_intersect(new_claim.globs, c.globs)`:
      - Two globs intersect iff there exists at least one path that matches
        both. Implementation: take the more specific of the two and probe
        the other; in practice use `doublestar.Match` against a synthetic
        witness derived from common literal prefixes/suffixes, plus a
        fallback that walks `git ls-files` and tests both patterns when the
        witness method is inconclusive. The exact heuristic is documented
        in code with tests.
   3. If they intersect:
      - If `new_claim.intent` ∈ {`edit`,`create`,`delete`} **or**
        `c.intent` ∈ {`edit`,`create`,`delete`} → `hard`.
      - Else (both `read`) → `soft`.
3. Aggregate the strongest verdict across all conflicting claims.

**Behavior in `claim add`**

- `hard` → block, exit code `3`, print conflicting agents and patterns.
- `soft` → warn, allow, record an event `claim.soft_overlap`.
- `none` → allow.

**Edge cases**

- Empty pattern → rejected at parse time.
- Glob that matches the entire repo (`**` alone) → rejected unless
  `--scope-of-the-universe-i-know` (intentionally undocumented; for tests).
- `module` claim referencing an unknown module → rejected with suggestion.

---

## 2. Stale agent detection

**Inputs**

- Agent `A` with `B_A`, branch `br_A`, active claims, `M(A)`.
- All other active agents.
- `base_branch` HEAD commit `H`.

**Outputs**

- `verdict`: `fresh | stale`.
- `reason`: structured.
- `recommendation`: `rebase | re-plan | review | narrow | abandon`.

**Procedure**

1. `base_changed = git diff --name-only <B_A>..<H>` (files changed on
   `base_branch` since `A` started).
2. Compute the watched file set for `A`:
   - `claimed = files matching any pattern in P(A)`.
   - `modified = M(A)`.
   - `watched = claimed ∪ modified`.
3. `intersection = base_changed ∩ watched`.
4. If `intersection` is non-empty → `stale`.
5. Choose `recommendation`:
   - `|intersection| <= 3` and all in `modified` only → `rebase`.
   - `|intersection| <= 3` and entirely in `claimed` not `modified` → `review`
     (the agent hasn't touched these yet; cheap to re-plan).
   - `|intersection| > 3` and modified set non-trivial → `re-plan`.
   - Forbidden path among `intersection` → `narrow`.
   - Agent has no commits and `|intersection|` is large → `abandon`
     suggestion.

**Resolution**

- `agentgrid refresh` re-evaluates and may set `resolved_at` automatically
  if the agent's branch contains the changed files (i.e., the agent rebased).
- Explicit `agentgrid stale resolve <agent> --reason "..."` is supported.

**Cost control**

- `refresh` runs once per agent serially in MVP. For N agents and small
  diffs, this is sub-second; we measure and revisit if it grows.

---

## 3. Diff-risk scoring

**Inputs**

- Diff snapshot for an agent (files changed, lines +/−, modules touched,
  forbidden hits, claim violations, has_tests).
- `config.diff_risk.thresholds`.

**Outputs**

- `level`: `low | medium | high`.
- `reasons`: list of structured codes with detail.

**Procedure**

A reason fires if the corresponding threshold is crossed. The `level` is the
maximum severity across firing reasons; reasons are not summed.

Reason codes:

| Code                       | Trigger                                                       | Severity |
| -------------------------- | ------------------------------------------------------------- | -------- |
| `files_over_low`           | `files_changed > files_low`                                   | low      |
| `files_over_medium`        | `files_changed > files_medium`                                | medium   |
| `files_over_high`          | `files_changed > files_high`                                  | high     |
| `lines_over_low`           | `lines_added + lines_removed > lines_low`                     | low      |
| `lines_over_medium`        | sum `> lines_medium`                                          | medium   |
| `lines_over_high`          | sum `> lines_high`                                            | high     |
| `modules_over_medium`      | `|modules_touched| >= modules_medium`                         | medium   |
| `modules_over_high`        | `|modules_touched| >= modules_high`                           | high     |
| `forbidden_path_touched`   | any file in `config.forbidden_paths`                          | high     |
| `claim_violation`          | any modified file matches no active claim of this agent       | medium   |
| `claim_violation_repeated` | same as above for ≥ 3 files                                   | high     |
| `no_tests_with_code`       | code changed but no test file matched `test_file_globs`       | medium   |
| `binary_files_touched`     | binary file in diff                                           | medium   |

**Output rendering**

- Human: `HIGH (files_over_high, forbidden_path_touched)`.
- JSON: full reasons array with detail (paths, counts, thresholds crossed).

**`pr` interaction**

- Default: `level >= config.diff_risk.require_force_at` blocks `pr` without
  `--force`. The user may supply `--reason "..."` to record an override note
  in `events`.

---

## 4. Forbidden path detection

- Globs from `config.forbidden_paths`.
- A file matches forbidden if any glob matches the file's repo-relative path.
- Used both during refresh (annotates the snapshot) and on `claim add`
  (refuses claims whose pattern is fully contained in a forbidden glob unless
  `--allow-forbidden` is passed and a reason is recorded).

---

## 5. PR size / risk warning

The PR readiness check is a thin layer over diff-risk:

**Procedure**

1. Recompute the current diff snapshot for the agent.
2. If `branch` has uncommitted changes → refuse.
3. If `stale_marks` open → refuse unless `--force`.
4. If `risk.level >= config.diff_risk.require_force_at` → refuse unless
   `--force`.
5. If `risk.reasons` contains `no_tests_with_code` → warn but do not refuse
   (this is a soft pressure, not a hard gate).
6. Render template; either print, write to file, or call `gh pr create`.

---

## 6. Status derivation (review queue buckets)

**Inputs per agent**

- `agent.status` (registered/working/blocked/abandoned/merged).
- `pr` row (if any) and its status.
- `current_stale` (if any).
- `current_diff` (if any) and its risk level.
- Branch state from git: `is_clean`, `ahead`, `behind` against `base_branch`.

**Buckets**

- `merged` — `agent.status = merged` or `pr.status = merged`.
- `abandoned` — `agent.status = abandoned`.
- `pr_open` — `pr.status = open` (sub-flag `pr_draft` for drafts).
- `ready_for_pr` — clean, `ahead > 0`, no stale, risk < high (or = high with
  prior override), no PR yet.
- `diff_too_large` — risk = high and no override and no PR.
- `stale` — current stale mark exists and unresolved.
- `blocked` — `agent.status = blocked`.
- `working` — `agent.status = working` and none of the above.
- `registered` — `agent.status = registered` (no branch yet or empty branch).

Buckets are mutually exclusive; the table above is evaluated top-down.

**Filtering in `agentgrid status`**

- `--bucket pr_open,ready_for_pr` etc.
- `agentgrid review` is sugar for
  `status --bucket ready_for_pr,diff_too_large,stale,blocked`.

---

## 7. Cleanup safety

`agentgrid cleanup` proposes deletions; it never deletes silently.

**Safe to remove**

- Worktree `W` is safe to remove iff:
  - the associated agent's branch is fully merged into `base_branch`
    (`git merge-base --is-ancestor <branch> <base_branch>` is true), or
  - the agent is explicitly `abandoned`,
  - AND the worktree has no uncommitted changes,
  - AND the worktree has no untracked files outside `.agentgrid/`.

**Safe to remove (branches)**

- Local branch `br` is safe iff its commits are reachable from `base_branch`
  (merged) or it has no commits ahead of base (empty), and no worktree
  references it.

**Procedure**

1. For each worktree, evaluate the predicates and collect a proposal list.
2. Print a table: `agent | path | branch | reason-safe`.
3. Prompt for confirmation (or `--yes` skips prompt).
4. On confirm: `git worktree remove`, then optionally `git branch -d`.
5. Soft-delete the `worktrees` row (`removed_at`) and write events.

**Hard refusals**

- Dirty worktree → refuse with the dirty paths.
- Branch not merged and not abandoned → refuse with the suggestion to mark
  the agent abandoned or merge first.
- Worktree not registered with AgentGrid → refuse; tell the user to run
  `git worktree remove` themselves (we do not touch unknown paths).
