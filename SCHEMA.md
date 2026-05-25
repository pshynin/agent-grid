# SCHEMA.md — AgentGrid SQLite schema

All timestamps are ISO-8601 strings in UTC (`YYYY-MM-DDTHH:MM:SSZ`). All IDs
are ULIDs stored as `TEXT` for human readability and lexical sortability;
they could be swapped for UUIDv7 without schema changes.

Pragmas applied on every open:

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
PRAGMA synchronous = NORMAL;
```

## 1. `schema_migrations`

```sql
CREATE TABLE schema_migrations (
  version    INTEGER PRIMARY KEY,
  applied_at TEXT NOT NULL
);
```

## 2. `meta`

Free-form key/value store. Used for repo root, schema version
double-check, defaults that are environment-sensitive.

```sql
CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
```

Seeded rows:

- `repo_root` — absolute path resolved at `init`.
- `default_base_branch` — copied from config at `init`; runtime reads config.

## 3. `agents`

```sql
CREATE TABLE agents (
  id            TEXT PRIMARY KEY,
  name          TEXT NOT NULL UNIQUE,
  role          TEXT NOT NULL,           -- architect|slice|reviewer|qa|security|other
  task          TEXT NOT NULL,           -- short human description
  task_brief    TEXT,                    -- optional path or inline brief
  runner        TEXT,                    -- claude|codex|gemini|cursor|manual|...
  owner         TEXT NOT NULL,           -- usually $USER
  status        TEXT NOT NULL,           -- registered|working|blocked|abandoned|merged
  base_branch   TEXT NOT NULL,
  branch        TEXT,                    -- set after spawn or adoption
  tmux_session  TEXT,                    -- optional
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

CREATE INDEX idx_agents_status ON agents(status);
CREATE INDEX idx_agents_owner  ON agents(owner);
```

`status` lifecycle:

```
registered -> working -> blocked|abandoned|merged
                     \-> working (resumed)
```

Derived buckets (stale, ready-for-review, diff-too-large, ...) are computed
in Go, not stored in `agents.status`.

## 4. `claims`

```sql
CREATE TABLE claims (
  id          TEXT PRIMARY KEY,
  agent_id    TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL,             -- path|module|glob
  pattern     TEXT NOT NULL,             -- the literal pattern stored
  intent      TEXT NOT NULL,             -- edit|read|create|delete
  risk        TEXT NOT NULL DEFAULT 'normal',  -- low|normal|high
  notes       TEXT,
  status      TEXT NOT NULL,             -- active|released|expired|superseded
  created_at  TEXT NOT NULL,
  expires_at  TEXT,
  released_at TEXT
);

CREATE INDEX idx_claims_agent_active
  ON claims(agent_id) WHERE status = 'active';
CREATE INDEX idx_claims_active
  ON claims(status) WHERE status = 'active';
```

Notes:

- `pattern` is interpreted by `kind`:
  - `path` → exact path (no globs).
  - `module` → resolved through `config.modules` to glob(s) at evaluation
    time; stored as the symbolic module name.
  - `glob` → doublestar pattern (`pkg/**/*.go`, `internal/auth/**`).
- Read claims do not conflict with each other.
- Edit/create/delete claims conflict with any overlapping claim including
  reads.

## 5. `worktrees`

```sql
CREATE TABLE worktrees (
  id           TEXT PRIMARY KEY,
  agent_id     TEXT NOT NULL UNIQUE
               REFERENCES agents(id) ON DELETE CASCADE,
  path         TEXT NOT NULL UNIQUE,
  branch       TEXT NOT NULL,
  base_branch  TEXT NOT NULL,
  base_commit  TEXT NOT NULL,             -- sha at creation; for stale calc
  created_at   TEXT NOT NULL,
  removed_at   TEXT                       -- soft-delete on cleanup
);
```

## 6. `diff_snapshots`

Append-only. The latest row per `agent_id` is the "current" snapshot.

```sql
CREATE TABLE diff_snapshots (
  id              TEXT PRIMARY KEY,
  agent_id        TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  head_commit     TEXT NOT NULL,
  base_commit     TEXT NOT NULL,
  files_changed   INTEGER NOT NULL,
  lines_added     INTEGER NOT NULL,
  lines_removed   INTEGER NOT NULL,
  modules_touched TEXT NOT NULL,          -- JSON array of module names
  touched_files   TEXT NOT NULL,          -- JSON array of paths
  forbidden_hits  TEXT NOT NULL,          -- JSON array of paths
  claim_violations TEXT NOT NULL,         -- JSON array of paths
  has_tests       INTEGER NOT NULL,       -- 0|1 heuristic
  risk_level      TEXT NOT NULL,          -- low|medium|high
  risk_reasons    TEXT NOT NULL,          -- JSON array of structured reasons
  taken_at        TEXT NOT NULL
);

CREATE INDEX idx_diffs_agent ON diff_snapshots(agent_id, taken_at DESC);
```

JSON arrays are denormalized on purpose: this is a snapshot record, not a
relational graph. Querying inside JSON is rare; rendering the snapshot is
the common path.

## 7. `stale_marks`

Append-only. "Current" mark per agent = most recent row with
`resolved_at IS NULL`.

```sql
CREATE TABLE stale_marks (
  id                  TEXT PRIMARY KEY,
  agent_id            TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  reason              TEXT NOT NULL,           -- short code
  detail              TEXT NOT NULL,           -- human sentence
  conflicting_agent   TEXT REFERENCES agents(id) ON DELETE SET NULL,
  conflicting_files   TEXT NOT NULL,           -- JSON array
  recommendation      TEXT NOT NULL,           -- rebase|re-plan|review|narrow|abandon
  created_at          TEXT NOT NULL,
  resolved_at         TEXT,
  resolved_reason     TEXT
);

CREATE INDEX idx_stale_open ON stale_marks(agent_id)
  WHERE resolved_at IS NULL;
```

Reason codes (extensible):

- `base_branch_advanced_into_claim`
- `base_branch_advanced_into_modified_set`
- `claim_overlap_detected_post_hoc`
- `worktree_dirty_after_rebase`

## 8. `events`

Append-only audit log. Every meaningful action writes one row.

```sql
CREATE TABLE events (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  ts          TEXT NOT NULL,
  agent_id    TEXT REFERENCES agents(id) ON DELETE SET NULL,
  actor       TEXT NOT NULL,                  -- usually $USER
  type        TEXT NOT NULL,                  -- enum, see below
  payload     TEXT NOT NULL                   -- JSON blob
);

CREATE INDEX idx_events_agent ON events(agent_id, ts DESC);
CREATE INDEX idx_events_type  ON events(type, ts DESC);
```

Event types (initial):

- `agent.added`, `agent.updated`, `agent.removed`
- `agent.status_changed`
- `claim.added`, `claim.released`, `claim.expired`, `claim.violation_detected`
- `worktree.created`, `worktree.removed`
- `refresh.started`, `refresh.finished`
- `stale.marked`, `stale.resolved`
- `diff.snapshot_taken`
- `pr.body_generated`, `pr.opened`
- `cleanup.proposed`, `cleanup.executed`
- `policy.refusal`                              -- e.g., HIGH risk PR blocked

## 9. `reviews`

For ingested reviewer/QA/security agent output. Optional in MVP; table exists
so the wiring is forward-compatible.

```sql
CREATE TABLE reviews (
  id          TEXT PRIMARY KEY,
  agent_id    TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL,                  -- code|qa|security|other
  status      TEXT NOT NULL,                  -- pass|warn|fail
  summary     TEXT NOT NULL,
  detail_path TEXT,                           -- path to a markdown file
  created_at  TEXT NOT NULL
);

CREATE INDEX idx_reviews_agent ON reviews(agent_id, created_at DESC);
```

## 10. `prs`

```sql
CREATE TABLE prs (
  id          TEXT PRIMARY KEY,
  agent_id    TEXT NOT NULL UNIQUE
              REFERENCES agents(id) ON DELETE CASCADE,
  number      INTEGER,                        -- nullable until opened
  url         TEXT,
  status      TEXT NOT NULL,                  -- draft|open|merged|closed|abandoned
  title       TEXT NOT NULL,
  body_path   TEXT,                           -- saved rendered body
  risk_level  TEXT NOT NULL,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);
```

## 11. Derived views (computed in Go, not SQL views)

- **`current_diff(agent_id)`** = most recent `diff_snapshots` row.
- **`current_stale(agent_id)`** = most recent `stale_marks` row with
  `resolved_at IS NULL`.
- **`active_claims(agent_id)`** = `claims WHERE status = 'active' AND
  (expires_at IS NULL OR expires_at > now())`.
- **`status_bucket(agent_id)`** = result of `policy.status.Derive` over the
  above plus the agent row.

These are not stored. Storing them invites drift.

## 12. Migration policy

- Forward-only; no down migrations.
- Each migration is `NNNN_description.sql` and is embedded with `embed.FS`.
- Migrations are idempotent where possible (`CREATE TABLE IF NOT EXISTS` is
  acceptable for the initial migration; later migrations are explicit).
- The binary refuses to run if `schema_migrations.version` is higher than
  the latest known migration — protects against downgrades.
- Within MVP, migrations stay additive (new tables, new nullable columns).
  Breaking changes wait for a documented major version.

## 13. Sample seed for development

```sql
INSERT INTO agents (id, name, role, task, owner, status,
                    base_branch, created_at, updated_at)
VALUES ('01J...A', 'billing-extract', 'slice',
        'Extract billing module', 'paul', 'registered',
        'main', '2026-05-24T12:00:00Z', '2026-05-24T12:00:00Z');

INSERT INTO claims (id, agent_id, kind, pattern, intent, status, created_at)
VALUES ('01J...B', '01J...A', 'glob', 'pkg/billing/**', 'edit',
        'active', '2026-05-24T12:00:00Z');
```
