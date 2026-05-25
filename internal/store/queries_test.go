package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/pshynin/agent-grid/internal/core"
)

func mustOpenTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sampleAgent(id, name string) core.Agent {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return core.Agent{
		ID:           id,
		Name:         name,
		Task:         "task " + name,
		Branch:       "feat/" + name,
		BaseBranch:   "main",
		BaseCommit:   "0000000000000000000000000000000000000000",
		WorktreePath: "",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func sampleClaim(id, agentID, pattern string, intent core.ClaimIntent) core.Claim {
	return core.Claim{
		ID:        id,
		AgentID:   agentID,
		Kind:      core.ClaimKindGlob,
		Pattern:   pattern,
		Intent:    intent,
		CreatedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
}

func TestCreateAgentAndGet(t *testing.T) {
	s := mustOpenTestStore(t)
	ctx := context.Background()

	a := sampleAgent("a1", "billing")
	a.WorktreePath = "/tmp/wt"
	if err := s.CreateAgent(ctx, a); err != nil {
		t.Fatalf("CreateAgent: %v", err)
	}

	got, err := s.GetAgentByName(ctx, "billing")
	if err != nil {
		t.Fatalf("GetAgentByName: %v", err)
	}
	if got.ID != a.ID || got.Name != a.Name || got.Task != a.Task ||
		got.Branch != a.Branch || got.BaseBranch != a.BaseBranch ||
		got.BaseCommit != a.BaseCommit || got.WorktreePath != a.WorktreePath {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, a)
	}
	if !got.CreatedAt.Equal(a.CreatedAt) {
		t.Errorf("CreatedAt: got %v, want %v", got.CreatedAt, a.CreatedAt)
	}

	got, err = s.GetAgentByID(ctx, "a1")
	if err != nil {
		t.Fatalf("GetAgentByID: %v", err)
	}
	if got.Name != "billing" {
		t.Errorf("GetAgentByID returned %q", got.Name)
	}
}

func TestGetAgentNotFound(t *testing.T) {
	s := mustOpenTestStore(t)
	_, err := s.GetAgentByName(context.Background(), "ghost")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestAgentNameUnique(t *testing.T) {
	s := mustOpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateAgent(ctx, sampleAgent("a1", "dup")); err != nil {
		t.Fatal(err)
	}
	err := s.CreateAgent(ctx, sampleAgent("a2", "dup"))
	if err == nil {
		t.Fatal("expected UNIQUE violation")
	}
}

func TestListAgentsOrderedByCreation(t *testing.T) {
	s := mustOpenTestStore(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Microsecond)
	for i, name := range []string{"a", "b", "c"} {
		a := sampleAgent("id"+name, name)
		a.CreatedAt = now.Add(time.Duration(i) * time.Second)
		a.UpdatedAt = a.CreatedAt
		if err := s.CreateAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d agents, want 3", len(got))
	}
	if got[0].Name != "a" || got[1].Name != "b" || got[2].Name != "c" {
		t.Errorf("order mismatch: %v %v %v", got[0].Name, got[1].Name, got[2].Name)
	}
}

func TestCreateClaimAndList(t *testing.T) {
	s := mustOpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateAgent(ctx, sampleAgent("a1", "billing")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAgent(ctx, sampleAgent("a2", "auth")); err != nil {
		t.Fatal(err)
	}

	claims := []core.Claim{
		sampleClaim("c1", "a1", "pkg/billing/**", core.ClaimIntentEdit),
		sampleClaim("c2", "a1", "internal/invoice/**", core.ClaimIntentRead),
		sampleClaim("c3", "a2", "pkg/auth/**", core.ClaimIntentEdit),
	}
	for _, c := range claims {
		if err := s.CreateClaim(ctx, c); err != nil {
			t.Fatalf("CreateClaim %s: %v", c.ID, err)
		}
	}

	all, err := s.ListClaims(ctx)
	if err != nil {
		t.Fatalf("ListClaims: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ListClaims returned %d, want 3", len(all))
	}

	byAgent, err := s.ListClaimsByAgent(ctx, "a1")
	if err != nil {
		t.Fatalf("ListClaimsByAgent: %v", err)
	}
	if len(byAgent) != 2 {
		t.Errorf("ListClaimsByAgent(a1) = %d, want 2", len(byAgent))
	}
}

func TestCreateAgentWithClaimsAtomic(t *testing.T) {
	s := mustOpenTestStore(t)
	ctx := context.Background()

	a1 := sampleAgent("a1", "agent")
	c1 := sampleClaim("c1", "a1", "x/**", core.ClaimIntentEdit)
	if err := s.CreateAgentWithClaims(ctx, a1, []core.Claim{c1}); err != nil {
		t.Fatalf("first CreateAgentWithClaims: %v", err)
	}

	// Attempt to insert a second agent with the same name; this must fail
	// on the UNIQUE constraint and roll back the would-be claim insert.
	a2 := sampleAgent("a2", "agent")
	c2 := sampleClaim("c2", "a2", "y/**", core.ClaimIntentEdit)
	err := s.CreateAgentWithClaims(ctx, a2, []core.Claim{c2})
	if err == nil {
		t.Fatal("expected UNIQUE violation")
	}

	all, err := s.ListClaims(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID != "c1" {
		t.Errorf("atomicity broken: claims = %+v", all)
	}
	agents, err := s.ListAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) != 1 || agents[0].ID != "a1" {
		t.Errorf("atomicity broken: agents = %+v", agents)
	}
}

func TestCreateAgentWithClaimsFKConstraint(t *testing.T) {
	s := mustOpenTestStore(t)
	ctx := context.Background()
	a := sampleAgent("a1", "agent")
	// Claim with wrong agent_id triggers FK violation; entire tx must roll back.
	c := sampleClaim("c1", "non-existent-agent", "x/**", core.ClaimIntentEdit)
	if err := s.CreateAgentWithClaims(ctx, a, []core.Claim{c}); err == nil {
		t.Fatal("expected FK violation")
	}
	agents, _ := s.ListAgents(ctx)
	if len(agents) != 0 {
		t.Errorf("expected no agents after rollback, got %+v", agents)
	}
}
