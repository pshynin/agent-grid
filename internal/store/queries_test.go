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

func TestReplaceStaleMarksForAgent(t *testing.T) {
	s := mustOpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateAgent(ctx, sampleAgent("a1", "agent")); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	mark := core.StaleMark{
		ID:               "m1",
		AgentID:          "a1",
		Reason:           "base advanced into claimed scope (2 files)",
		ConflictingFiles: []string{"pkg/billing/types.go", "pkg/billing/api.go"},
		Recommendation:   core.RecommendReview,
		CreatedAt:        now,
	}
	if err := s.ReplaceStaleMarksForAgent(ctx, "a1", []core.StaleMark{mark}); err != nil {
		t.Fatalf("ReplaceStaleMarksForAgent: %v", err)
	}

	got, err := s.ListStaleMarks(ctx)
	if err != nil {
		t.Fatalf("ListStaleMarks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d marks, want 1", len(got))
	}
	g := got[0]
	if g.ID != mark.ID || g.AgentID != mark.AgentID || g.Reason != mark.Reason ||
		g.Recommendation != mark.Recommendation {
		t.Errorf("scalar mismatch: got %+v", g)
	}
	if len(g.ConflictingFiles) != 2 ||
		g.ConflictingFiles[0] != mark.ConflictingFiles[0] ||
		g.ConflictingFiles[1] != mark.ConflictingFiles[1] {
		t.Errorf("files mismatch: %v", g.ConflictingFiles)
	}

	// Replace with a different mark; the old one must be gone.
	mark2 := mark
	mark2.ID = "m2"
	mark2.Reason = "second"
	if err := s.ReplaceStaleMarksForAgent(ctx, "a1", []core.StaleMark{mark2}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ListStaleMarks(ctx)
	if len(got) != 1 || got[0].ID != "m2" {
		t.Errorf("replacement failed: %+v", got)
	}

	// Clear by passing empty slice.
	if err := s.ReplaceStaleMarksForAgent(ctx, "a1", nil); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ListStaleMarks(ctx)
	if len(got) != 0 {
		t.Errorf("expected zero marks after clear, got %+v", got)
	}
}

func TestStaleMarksCascadeOnAgentDelete(t *testing.T) {
	s := mustOpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateAgent(ctx, sampleAgent("a1", "agent")); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := s.ReplaceStaleMarksForAgent(ctx, "a1", []core.StaleMark{{
		ID: "m1", AgentID: "a1", Reason: "r",
		ConflictingFiles: []string{"x"}, Recommendation: core.RecommendRebase,
		CreatedAt: now,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `DELETE FROM agents WHERE id = ?`, "a1"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.ListStaleMarks(ctx)
	if len(got) != 0 {
		t.Errorf("FK cascade expected; got %+v", got)
	}
}

func TestCreateAndReadLatestDiffSnapshot(t *testing.T) {
	s := mustOpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateAgent(ctx, sampleAgent("a1", "billing")); err != nil {
		t.Fatal(err)
	}

	_, err := s.LatestDiffSnapshotByAgent(ctx, "a1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound when no snapshot, got %v", err)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	d := core.DiffSnapshot{
		ID:              "d1",
		AgentID:         "a1",
		HeadCommit:      "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		FilesChanged:    3,
		LinesAdded:      120,
		LinesRemoved:    40,
		TouchedFiles:    []string{"pkg/billing/types.go", "pkg/billing/api.go"},
		ForbiddenHits:   []string{},
		ClaimViolations: []string{},
		RiskLevel:       core.RiskLow,
		RiskReasons:     []core.Reason{},
		TakenAt:         now,
	}
	if err := s.CreateDiffSnapshot(ctx, d); err != nil {
		t.Fatalf("CreateDiffSnapshot: %v", err)
	}
	got, err := s.LatestDiffSnapshotByAgent(ctx, "a1")
	if err != nil {
		t.Fatalf("LatestDiffSnapshotByAgent: %v", err)
	}
	if got.HeadCommit != d.HeadCommit || got.FilesChanged != d.FilesChanged ||
		got.LinesAdded != d.LinesAdded || got.LinesRemoved != d.LinesRemoved ||
		got.RiskLevel != d.RiskLevel {
		t.Errorf("round-trip scalar mismatch: %+v vs %+v", got, d)
	}
	if len(got.TouchedFiles) != 2 || got.TouchedFiles[0] != "pkg/billing/types.go" {
		t.Errorf("touched_files: %v", got.TouchedFiles)
	}

	// Insert a second, newer snapshot; latest should reflect it.
	d2 := d
	d2.ID = "d2"
	d2.RiskLevel = core.RiskHigh
	d2.TakenAt = now.Add(time.Second)
	d2.RiskReasons = []core.Reason{{
		Code: "files_over_high", Severity: core.SeverityHigh,
		Detail: "31 files (>30)",
	}}
	if err := s.CreateDiffSnapshot(ctx, d2); err != nil {
		t.Fatal(err)
	}
	got, err = s.LatestDiffSnapshotByAgent(ctx, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "d2" || got.RiskLevel != core.RiskHigh {
		t.Errorf("latest should be d2/high, got %+v", got)
	}
	if len(got.RiskReasons) != 1 || got.RiskReasons[0].Code != "files_over_high" {
		t.Errorf("RiskReasons round-trip wrong: %+v", got.RiskReasons)
	}
}

func TestDiffSnapshotsCascadeOnAgentDelete(t *testing.T) {
	s := mustOpenTestStore(t)
	ctx := context.Background()
	if err := s.CreateAgent(ctx, sampleAgent("a1", "agent")); err != nil {
		t.Fatal(err)
	}
	d := core.DiffSnapshot{
		ID: "d1", AgentID: "a1",
		HeadCommit: "0000", FilesChanged: 1, LinesAdded: 1, LinesRemoved: 0,
		TouchedFiles: []string{"a"}, ForbiddenHits: []string{},
		ClaimViolations: []string{}, RiskLevel: core.RiskLow,
		RiskReasons: []core.Reason{}, TakenAt: time.Now().UTC(),
	}
	if err := s.CreateDiffSnapshot(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `DELETE FROM agents WHERE id = ?`, "a1"); err != nil {
		t.Fatal(err)
	}
	_, err := s.LatestDiffSnapshotByAgent(ctx, "a1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected cascade delete, got %v", err)
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
