package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/pshynin/agent-grid/internal/core"
)

// ErrNotFound is returned when a lookup finds no matching row.
var ErrNotFound = errors.New("not found")

const timeFmt = time.RFC3339Nano

// --- Agents ----------------------------------------------------------------

func (s *Store) CreateAgent(ctx context.Context, a core.Agent) error {
	return execWithErrContext(s.db.ExecContext(ctx,
		`INSERT INTO agents
		   (id, name, task, branch, base_branch, base_commit, worktree_path,
		    created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.Task, a.Branch, a.BaseBranch, a.BaseCommit,
		nullableString(a.WorktreePath),
		a.CreatedAt.UTC().Format(timeFmt),
		a.UpdatedAt.UTC().Format(timeFmt),
	))
}

func (s *Store) GetAgentByName(ctx context.Context, name string) (core.Agent, error) {
	return scanAgent(s.db.QueryRowContext(ctx, agentSelectByName, name))
}

func (s *Store) GetAgentByID(ctx context.Context, id string) (core.Agent, error) {
	return scanAgent(s.db.QueryRowContext(ctx, agentSelectByID, id))
}

func (s *Store) ListAgents(ctx context.Context) ([]core.Agent, error) {
	rows, err := s.db.QueryContext(ctx, agentSelectAll)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	var out []core.Agent
	for rows.Next() {
		a, err := scanAgentRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// --- Claims ----------------------------------------------------------------

func (s *Store) CreateClaim(ctx context.Context, c core.Claim) error {
	return execWithErrContext(s.db.ExecContext(ctx,
		`INSERT INTO claims (id, agent_id, kind, pattern, intent, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		c.ID, c.AgentID, string(c.Kind), c.Pattern, string(c.Intent),
		c.CreatedAt.UTC().Format(timeFmt),
	))
}

func (s *Store) ListClaims(ctx context.Context) ([]core.Claim, error) {
	rows, err := s.db.QueryContext(ctx, claimSelectAll)
	if err != nil {
		return nil, fmt.Errorf("list claims: %w", err)
	}
	defer rows.Close()
	var out []core.Claim
	for rows.Next() {
		c, err := scanClaim(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ListClaimsByAgent(ctx context.Context, agentID string) ([]core.Claim, error) {
	rows, err := s.db.QueryContext(ctx, claimSelectByAgent, agentID)
	if err != nil {
		return nil, fmt.Errorf("list claims for agent %s: %w", agentID, err)
	}
	defer rows.Close()
	var out []core.Claim
	for rows.Next() {
		c, err := scanClaim(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// --- Composite -------------------------------------------------------------

// CreateAgentWithClaims inserts the agent and the given claims atomically.
// If any insert fails the whole transaction is rolled back, so neither the
// agent nor its claims end up in the database.
func (s *Store) CreateAgentWithClaims(ctx context.Context, a core.Agent, claims []core.Claim) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO agents
		   (id, name, task, branch, base_branch, base_commit, worktree_path,
		    created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.Name, a.Task, a.Branch, a.BaseBranch, a.BaseCommit,
		nullableString(a.WorktreePath),
		a.CreatedAt.UTC().Format(timeFmt),
		a.UpdatedAt.UTC().Format(timeFmt),
	); err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, c := range claims {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO claims (id, agent_id, kind, pattern, intent, created_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			c.ID, c.AgentID, string(c.Kind), c.Pattern, string(c.Intent),
			c.CreatedAt.UTC().Format(timeFmt),
		); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// --- helpers ---------------------------------------------------------------

const (
	agentCols    = `id, name, task, branch, base_branch, base_commit, worktree_path, created_at, updated_at`
	claimCols    = `id, agent_id, kind, pattern, intent, created_at`

	agentSelectAll    = `SELECT ` + agentCols + ` FROM agents ORDER BY created_at ASC, id ASC`
	agentSelectByName = `SELECT ` + agentCols + ` FROM agents WHERE name = ?`
	agentSelectByID   = `SELECT ` + agentCols + ` FROM agents WHERE id = ?`

	claimSelectAll     = `SELECT ` + claimCols + ` FROM claims ORDER BY created_at ASC, id ASC`
	claimSelectByAgent = `SELECT ` + claimCols + ` FROM claims WHERE agent_id = ? ORDER BY created_at ASC, id ASC`
)

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAgent(row *sql.Row) (core.Agent, error) {
	a, err := scanAgentRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Agent{}, ErrNotFound
	}
	return a, err
}

func scanAgentRow(r rowScanner) (core.Agent, error) {
	var a core.Agent
	var worktree sql.NullString
	var createdAt, updatedAt string
	if err := r.Scan(&a.ID, &a.Name, &a.Task, &a.Branch, &a.BaseBranch,
		&a.BaseCommit, &worktree, &createdAt, &updatedAt); err != nil {
		return core.Agent{}, err
	}
	if worktree.Valid {
		a.WorktreePath = worktree.String
	}
	t, err := time.Parse(timeFmt, createdAt)
	if err != nil {
		return core.Agent{}, fmt.Errorf("parse created_at: %w", err)
	}
	a.CreatedAt = t
	t, err = time.Parse(timeFmt, updatedAt)
	if err != nil {
		return core.Agent{}, fmt.Errorf("parse updated_at: %w", err)
	}
	a.UpdatedAt = t
	return a, nil
}

func scanClaim(r rowScanner) (core.Claim, error) {
	var c core.Claim
	var kind, intent, createdAt string
	if err := r.Scan(&c.ID, &c.AgentID, &kind, &c.Pattern, &intent, &createdAt); err != nil {
		return core.Claim{}, err
	}
	c.Kind = core.ClaimKind(kind)
	c.Intent = core.ClaimIntent(intent)
	t, err := time.Parse(timeFmt, createdAt)
	if err != nil {
		return core.Claim{}, fmt.Errorf("parse created_at: %w", err)
	}
	c.CreatedAt = t
	return c, nil
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func execWithErrContext(res sql.Result, err error) error {
	_ = res
	return err
}
