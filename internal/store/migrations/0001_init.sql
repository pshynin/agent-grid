CREATE TABLE IF NOT EXISTS agents (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL UNIQUE,
    task          TEXT NOT NULL,
    branch        TEXT NOT NULL,
    base_branch   TEXT NOT NULL,
    base_commit   TEXT NOT NULL,
    worktree_path TEXT,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS claims (
    id         TEXT PRIMARY KEY,
    agent_id   TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    pattern    TEXT NOT NULL,
    intent     TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_claims_agent ON claims(agent_id);

CREATE TABLE IF NOT EXISTS diff_snapshots (
    id               TEXT PRIMARY KEY,
    agent_id         TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    head_commit      TEXT NOT NULL,
    files_changed    INTEGER NOT NULL,
    lines_added      INTEGER NOT NULL,
    lines_removed    INTEGER NOT NULL,
    touched_files    TEXT NOT NULL,
    forbidden_hits   TEXT NOT NULL,
    claim_violations TEXT NOT NULL,
    risk_level       TEXT NOT NULL,
    risk_reasons     TEXT NOT NULL,
    taken_at         TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_diffs_agent ON diff_snapshots(agent_id, taken_at DESC);

CREATE TABLE IF NOT EXISTS stale_marks (
    id                TEXT PRIMARY KEY,
    agent_id          TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    reason            TEXT NOT NULL,
    conflicting_files TEXT NOT NULL,
    recommendation    TEXT NOT NULL,
    created_at        TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_stale_agent ON stale_marks(agent_id, created_at DESC);
