package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pshynin/agent-grid/internal/store"
)

// newTestRepo creates a git repo with `main` (one commit) and `feat/billing`
// (one extra commit), then checks `main` out.
func newTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-q", "-m", "init")
	run("checkout", "-q", "-b", "feat/billing")
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "b.txt")
	run("commit", "-q", "-m", "feat work")
	run("checkout", "-q", "main")
	return dir
}

// runCLI executes the command tree against args. It returns combined stdout +
// stderr (cobra writes errors via the user's stream when SetErr is wired) and
// the exit code that main would produce.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	cmd := NewRootCmd()
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	cmd.SetContext(context.Background())
	err := cmd.Execute()
	if err != nil {
		fmt.Fprintf(&errBuf, "error: %v\n", err)
	}
	exit = ExitCode(err)
	return outBuf.String(), errBuf.String(), exit
}

// runMustOK fails the test if the CLI exits non-zero.
func runMustOK(t *testing.T, args ...string) string {
	t.Helper()
	stdout, stderr, exit := runCLI(t, args...)
	if exit != 0 {
		t.Fatalf("agentgrid %s: exit %d\nstdout: %s\nstderr: %s",
			strings.Join(args, " "), exit, stdout, stderr)
	}
	return stdout
}

// openTestStore opens the SQLite DB created by `agentgrid init` so tests can
// make assertions against it directly.
func openTestStore(t *testing.T, repo string) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(repo, ".agentgrid", "state.db"))
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func initInRepo(t *testing.T) string {
	t.Helper()
	repo := newTestRepo(t)
	t.Chdir(repo)
	runMustOK(t, "init")
	return repo
}

// -------------------------------------------------------------------- agent --

func TestAgentAddWithInlineClaim(t *testing.T) {
	repo := initInRepo(t)

	stdout := runMustOK(t, "agent", "add",
		"--name", "billing", "--task", "extract billing",
		"--branch", "feat/billing",
		"--claim", "glob:pkg/billing/**:edit",
		"--claim", "glob:internal/invoice/**:read",
	)
	if !strings.Contains(stdout, "agent added: billing") {
		t.Errorf("unexpected output:\n%s", stdout)
	}

	s := openTestStore(t, repo)
	agents, _ := s.ListAgents(context.Background())
	if len(agents) != 1 || agents[0].Name != "billing" {
		t.Fatalf("agents = %+v", agents)
	}
	if agents[0].BaseBranch != "main" {
		t.Errorf("BaseBranch = %q, want main", agents[0].BaseBranch)
	}
	if len(agents[0].BaseCommit) != 40 {
		t.Errorf("BaseCommit not a sha: %q", agents[0].BaseCommit)
	}
	claims, _ := s.ListClaimsByAgent(context.Background(), agents[0].ID)
	if len(claims) != 2 {
		t.Errorf("got %d claims, want 2", len(claims))
	}
}

func TestAgentAddWithoutClaims(t *testing.T) {
	_ = initInRepo(t)
	runMustOK(t, "agent", "add",
		"--name", "x", "--task", "t", "--branch", "feat/billing")
}

func TestAgentAddMissingBranchFails(t *testing.T) {
	repo := initInRepo(t)
	_, stderr, exit := runCLI(t, "agent", "add",
		"--name", "x", "--task", "t", "--branch", "does-not-exist")
	if exit != 1 {
		t.Errorf("exit = %d, want 1; stderr=%s", exit, stderr)
	}
	if !strings.Contains(stderr, "does not exist") {
		t.Errorf("stderr should mention missing branch: %s", stderr)
	}
	// Nothing was written.
	s := openTestStore(t, repo)
	agents, _ := s.ListAgents(context.Background())
	if len(agents) != 0 {
		t.Errorf("agents should be empty: %+v", agents)
	}
}

func TestAgentAddMissingBaseFails(t *testing.T) {
	_ = initInRepo(t)
	_, stderr, exit := runCLI(t, "agent", "add",
		"--name", "x", "--task", "t",
		"--branch", "feat/billing", "--base", "no-such-base")
	if exit != 1 {
		t.Errorf("exit = %d, want 1; stderr=%s", exit, stderr)
	}
	if !strings.Contains(stderr, "base branch") {
		t.Errorf("stderr should mention base branch: %s", stderr)
	}
}

func TestAgentAddOverlappingClaimExitsThreeAndWritesNothing(t *testing.T) {
	repo := initInRepo(t)
	runMustOK(t, "agent", "add",
		"--name", "a1", "--task", "first", "--branch", "feat/billing",
		"--claim", "glob:pkg/billing/**:edit",
	)

	_, stderr, exit := runCLI(t, "agent", "add",
		"--name", "a2", "--task", "second", "--branch", "feat/billing",
		"--claim", "glob:pkg/billing/sub/**:edit",
	)
	if exit != 3 {
		t.Errorf("exit = %d, want 3 (policy refusal); stderr=%s", exit, stderr)
	}
	if !strings.Contains(stderr, "conflicts") || !strings.Contains(stderr, "a1 holds") {
		t.Errorf("stderr should report the conflicting agent: %s", stderr)
	}

	s := openTestStore(t, repo)
	agents, _ := s.ListAgents(context.Background())
	if len(agents) != 1 || agents[0].Name != "a1" {
		t.Errorf("a2 should not exist; got: %+v", agents)
	}
	claims, _ := s.ListClaims(context.Background())
	if len(claims) != 1 {
		t.Errorf("only a1's claim should remain; got %d", len(claims))
	}
}

func TestAgentAddReadReadOverlapAllowed(t *testing.T) {
	repo := initInRepo(t)
	runMustOK(t, "agent", "add",
		"--name", "a1", "--task", "t", "--branch", "feat/billing",
		"--claim", "glob:pkg/billing/**:read",
	)
	runMustOK(t, "agent", "add",
		"--name", "a2", "--task", "t", "--branch", "feat/billing",
		"--claim", "glob:pkg/billing/**:read",
	)
	s := openTestStore(t, repo)
	agents, _ := s.ListAgents(context.Background())
	if len(agents) != 2 {
		t.Errorf("want 2 agents, got %d", len(agents))
	}
}

func TestAgentAddSameAgentOverlapAllowed(t *testing.T) {
	// Adding two claims for the SAME new agent that overlap each other is
	// fine: same-agent overlap is never a conflict.
	_ = initInRepo(t)
	runMustOK(t, "agent", "add",
		"--name", "a1", "--task", "t", "--branch", "feat/billing",
		"--claim", "glob:pkg/billing/**:edit",
		"--claim", "glob:pkg/billing/sub/**:edit",
	)
}

func TestAgentAddIsAtomicOnConflict(t *testing.T) {
	// If one of N inline claims conflicts, NONE of them and not the agent
	// itself should be written.
	repo := initInRepo(t)
	runMustOK(t, "agent", "add",
		"--name", "owner", "--task", "t", "--branch", "feat/billing",
		"--claim", "glob:pkg/billing/**:edit",
	)
	_, _, exit := runCLI(t, "agent", "add",
		"--name", "newcomer", "--task", "t", "--branch", "feat/billing",
		"--claim", "glob:pkg/auth/**:edit",        // would be fine alone
		"--claim", "glob:pkg/billing/sub/**:edit", // conflict
	)
	if exit != 3 {
		t.Errorf("exit = %d, want 3", exit)
	}
	s := openTestStore(t, repo)
	agents, _ := s.ListAgents(context.Background())
	if len(agents) != 1 || agents[0].Name != "owner" {
		t.Errorf("newcomer must not exist; got %+v", agents)
	}
	claims, _ := s.ListClaims(context.Background())
	if len(claims) != 1 {
		t.Errorf("only owner's claim should remain; got %d", len(claims))
	}
}

func TestAgentAddDuplicateNameFails(t *testing.T) {
	repo := initInRepo(t)
	runMustOK(t, "agent", "add", "--name", "dup", "--task", "t", "--branch", "feat/billing")
	_, stderr, exit := runCLI(t, "agent", "add", "--name", "dup", "--task", "t", "--branch", "feat/billing")
	if exit != 1 {
		t.Errorf("exit = %d, want 1; stderr=%s", exit, stderr)
	}
	if !strings.Contains(stderr, "already in use") {
		t.Errorf("stderr should mention name in use: %s", stderr)
	}
	s := openTestStore(t, repo)
	agents, _ := s.ListAgents(context.Background())
	if len(agents) != 1 {
		t.Errorf("want 1 agent, got %d", len(agents))
	}
}

func TestAgentList(t *testing.T) {
	_ = initInRepo(t)
	runMustOK(t, "agent", "add", "--name", "a1", "--task", "t1", "--branch", "feat/billing")
	runMustOK(t, "agent", "add", "--name", "a2", "--task", "t2", "--branch", "feat/billing")

	stdout := runMustOK(t, "agent", "list")
	if !strings.Contains(stdout, "a1") || !strings.Contains(stdout, "a2") {
		t.Errorf("output should list both: %s", stdout)
	}
	if !strings.Contains(stdout, "NAME") {
		t.Errorf("output should have headers: %s", stdout)
	}
}

func TestAgentListJSON(t *testing.T) {
	_ = initInRepo(t)
	runMustOK(t, "agent", "add", "--name", "a1", "--task", "t", "--branch", "feat/billing",
		"--claim", "glob:pkg/billing/**:edit")

	stdout := runMustOK(t, "agent", "list", "--json")
	var got []map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if len(got) != 1 || got[0]["name"] != "a1" {
		t.Errorf("JSON contents wrong: %+v", got)
	}
	if claims, _ := got[0]["claims"].([]any); len(claims) != 1 {
		t.Errorf("expected 1 claim in JSON, got %v", got[0]["claims"])
	}
}

func TestAgentShow(t *testing.T) {
	_ = initInRepo(t)
	runMustOK(t, "agent", "add", "--name", "a1", "--task", "the task",
		"--branch", "feat/billing",
		"--claim", "glob:pkg/billing/**:edit")

	stdout := runMustOK(t, "agent", "show", "a1")
	for _, want := range []string{"a1", "the task", "feat/billing", "claims (1)"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("show output missing %q:\n%s", want, stdout)
		}
	}

	stdoutJSON := runMustOK(t, "agent", "show", "a1", "--json")
	var v map[string]any
	if err := json.Unmarshal([]byte(stdoutJSON), &v); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdoutJSON)
	}
	if v["name"] != "a1" {
		t.Errorf("JSON name wrong: %v", v["name"])
	}
}

func TestAgentShowNotFound(t *testing.T) {
	_ = initInRepo(t)
	_, stderr, exit := runCLI(t, "agent", "show", "ghost")
	if exit != 1 {
		t.Errorf("exit = %d, want 1", exit)
	}
	if !strings.Contains(stderr, "not found") {
		t.Errorf("stderr should mention not found: %s", stderr)
	}
}

// -------------------------------------------------------------------- claim --

func TestClaimAdd(t *testing.T) {
	repo := initInRepo(t)
	runMustOK(t, "agent", "add", "--name", "a1", "--task", "t", "--branch", "feat/billing")

	stdout := runMustOK(t, "claim", "add", "a1",
		"glob:pkg/billing/**:edit",
		"path:pkg/auth/session.go:read")
	if !strings.Contains(stdout, "2 claim(s) added to a1") {
		t.Errorf("unexpected output: %s", stdout)
	}

	s := openTestStore(t, repo)
	a, _ := s.GetAgentByName(context.Background(), "a1")
	cs, _ := s.ListClaimsByAgent(context.Background(), a.ID)
	if len(cs) != 2 {
		t.Errorf("got %d claims, want 2", len(cs))
	}
}

func TestClaimAddConflictExitsThree(t *testing.T) {
	repo := initInRepo(t)
	runMustOK(t, "agent", "add", "--name", "owner", "--task", "t", "--branch", "feat/billing",
		"--claim", "glob:pkg/billing/**:edit")
	runMustOK(t, "agent", "add", "--name", "newcomer", "--task", "t", "--branch", "feat/billing")

	_, stderr, exit := runCLI(t, "claim", "add", "newcomer", "glob:pkg/billing/sub/**:edit")
	if exit != 3 {
		t.Errorf("exit = %d, want 3; stderr=%s", exit, stderr)
	}
	if !strings.Contains(stderr, "owner holds") {
		t.Errorf("stderr should name the conflicting agent: %s", stderr)
	}

	s := openTestStore(t, repo)
	a, _ := s.GetAgentByName(context.Background(), "newcomer")
	cs, _ := s.ListClaimsByAgent(context.Background(), a.ID)
	if len(cs) != 0 {
		t.Errorf("newcomer should have no claims, got %+v", cs)
	}
}

func TestClaimAddReadReadAllowed(t *testing.T) {
	_ = initInRepo(t)
	runMustOK(t, "agent", "add", "--name", "a1", "--task", "t", "--branch", "feat/billing",
		"--claim", "glob:pkg/billing/**:read")
	runMustOK(t, "agent", "add", "--name", "a2", "--task", "t", "--branch", "feat/billing")
	runMustOK(t, "claim", "add", "a2", "glob:pkg/billing/**:read")
}

func TestClaimList(t *testing.T) {
	_ = initInRepo(t)
	runMustOK(t, "agent", "add", "--name", "a1", "--task", "t", "--branch", "feat/billing",
		"--claim", "glob:pkg/billing/**:edit")
	runMustOK(t, "agent", "add", "--name", "a2", "--task", "t", "--branch", "feat/billing",
		"--claim", "glob:pkg/auth/**:edit")

	stdout := runMustOK(t, "claim", "list")
	for _, want := range []string{"a1", "a2", "pkg/billing/**", "pkg/auth/**"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}

	stdout = runMustOK(t, "claim", "list", "--agent", "a1")
	if !strings.Contains(stdout, "pkg/billing/**") || strings.Contains(stdout, "pkg/auth/**") {
		t.Errorf("--agent filter wrong:\n%s", stdout)
	}
}

func TestClaimListJSON(t *testing.T) {
	_ = initInRepo(t)
	runMustOK(t, "agent", "add", "--name", "a1", "--task", "t", "--branch", "feat/billing",
		"--claim", "glob:pkg/billing/**:edit")
	stdout := runMustOK(t, "claim", "list", "--json")
	var got []map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if len(got) != 1 || got[0]["pattern"] != "pkg/billing/**" {
		t.Errorf("JSON wrong: %+v", got)
	}
}

func TestClaimCheckNoConflict(t *testing.T) {
	_ = initInRepo(t)
	stdout, _, exit := runCLI(t, "claim", "check", "glob:pkg/billing/**:edit")
	if exit != 0 {
		t.Errorf("exit = %d, want 0", exit)
	}
	if !strings.Contains(stdout, "no conflicts") {
		t.Errorf("output should be 'no conflicts': %s", stdout)
	}
}

func TestClaimCheckConflictDoesNotWrite(t *testing.T) {
	repo := initInRepo(t)
	runMustOK(t, "agent", "add", "--name", "owner", "--task", "t", "--branch", "feat/billing",
		"--claim", "glob:pkg/billing/**:edit")

	_, stderr, exit := runCLI(t, "claim", "check", "glob:pkg/billing/sub/**:edit")
	if exit != 3 {
		t.Errorf("exit = %d, want 3; stderr=%s", exit, stderr)
	}
	if !strings.Contains(stderr, "owner holds") {
		t.Errorf("stderr should name conflict: %s", stderr)
	}

	// Crucially: no new claim or agent was inserted by `check`.
	s := openTestStore(t, repo)
	agents, _ := s.ListAgents(context.Background())
	if len(agents) != 1 {
		t.Errorf("agents count changed: %+v", agents)
	}
	claims, _ := s.ListClaims(context.Background())
	if len(claims) != 1 {
		t.Errorf("claims count changed: %+v", claims)
	}
}

func TestClaimCheckJSON(t *testing.T) {
	_ = initInRepo(t)
	runMustOK(t, "agent", "add", "--name", "owner", "--task", "t", "--branch", "feat/billing",
		"--claim", "glob:pkg/billing/**:edit")

	stdout, _, exit := runCLI(t, "claim", "check", "glob:pkg/billing/sub/**:edit", "--json")
	if exit != 3 {
		t.Errorf("exit = %d, want 3", exit)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if v["conflict"] != true {
		t.Errorf("conflict field should be true: %+v", v)
	}
}

func TestRequiresInit(t *testing.T) {
	repo := newTestRepo(t)
	t.Chdir(repo)
	_, stderr, exit := runCLI(t, "agent", "list")
	if exit != 1 {
		t.Errorf("exit = %d, want 1", exit)
	}
	if !strings.Contains(stderr, "init") {
		t.Errorf("stderr should suggest init: %s", stderr)
	}
}

func TestInvalidClaimSpec(t *testing.T) {
	_ = initInRepo(t)
	_, stderr, exit := runCLI(t, "agent", "add",
		"--name", "a", "--task", "t", "--branch", "feat/billing",
		"--claim", "not-a-claim")
	if exit != 1 {
		t.Errorf("exit = %d, want 1", exit)
	}
	if !strings.Contains(stderr, "claim") {
		t.Errorf("stderr should mention claim parse error: %s", stderr)
	}
}

func TestInvalidClaimKind(t *testing.T) {
	_ = initInRepo(t)
	_, stderr, exit := runCLI(t, "agent", "add",
		"--name", "a", "--task", "t", "--branch", "feat/billing",
		"--claim", "module:billing:edit")
	if exit != 1 {
		t.Errorf("exit = %d, want 1; stderr=%s", exit, stderr)
	}
	if !strings.Contains(stderr, "kind") {
		t.Errorf("stderr should mention kind: %s", stderr)
	}
}

// Sanity check the rendering helpers don't choke on edge cases.
func TestAgentShowEmptyClaims(t *testing.T) {
	_ = initInRepo(t)
	runMustOK(t, "agent", "add", "--name", "x", "--task", "t", "--branch", "feat/billing")
	stdout := runMustOK(t, "agent", "show", "x")
	if !strings.Contains(stdout, "claims (0)") {
		t.Errorf("expected 'claims (0)' in show output:\n%s", stdout)
	}
}

// ---------------------------------------------------------- refresh / stale --

// newStaleScenarioRepo sets up a repo where:
//   - main has one initial commit X (the file `pkg/billing/types.go` exists).
//   - branch `agent-c` is created at X and immediately checked back out
//     leaving us on main.
//   - main advances by one commit that modifies `pkg/billing/types.go`,
//     simulating agent A having merged into main.
// The returned dir is the repo root; the test should `t.Chdir(dir)` after.
func newStaleScenarioRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.MkdirAll(filepath.Join(dir, "pkg/billing"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg/billing/types.go"),
		[]byte("package billing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg/auth.go"),
		[]byte("package auth\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "init")

	// Branch agent-c off the initial commit and switch back to main.
	run("checkout", "-q", "-b", "agent-c")
	run("checkout", "-q", "main")

	// Agent A's effect: a new commit on main touching pkg/billing/types.go.
	if err := os.WriteFile(filepath.Join(dir, "pkg/billing/types.go"),
		[]byte("package billing\n// changed by A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "A modifies types")
	return dir
}

// gitInRepo runs a git command in dir; fails the test on non-zero exit.
func gitInRepo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func TestRefreshMarksAndClearsStale(t *testing.T) {
	dir := newStaleScenarioRepo(t)
	t.Chdir(dir)
	runMustOK(t, "init")
	runMustOK(t, "agent", "add",
		"--name", "C", "--task", "be the second agent",
		"--branch", "agent-c",
		"--claim", "glob:pkg/billing/**:read",
	)

	// First refresh: C should be stale with one conflicting file and the
	// 'review' recommendation (read-only claim).
	stdout := runMustOK(t, "refresh", "--json")
	var rj struct {
		Refreshed []refreshResult `json:"refreshed"`
	}
	if err := json.Unmarshal([]byte(stdout), &rj); err != nil {
		t.Fatalf("invalid refresh JSON: %v\n%s", err, stdout)
	}
	if len(rj.Refreshed) != 1 {
		t.Fatalf("got %d refreshed agents, want 1", len(rj.Refreshed))
	}
	got := rj.Refreshed[0]
	if got.Agent != "C" || !got.Stale {
		t.Errorf("first refresh: %+v", got)
	}
	if got.Recommendation != "review" {
		t.Errorf("recommendation = %q, want review", got.Recommendation)
	}
	if len(got.ConflictingFiles) != 1 || got.ConflictingFiles[0] != "pkg/billing/types.go" {
		t.Errorf("conflicting files = %v", got.ConflictingFiles)
	}

	// `stale` command should list C.
	stdoutS := runMustOK(t, "stale", "--json")
	var sv []staleView
	if err := json.Unmarshal([]byte(stdoutS), &sv); err != nil {
		t.Fatalf("invalid stale JSON: %v\n%s", err, stdoutS)
	}
	if len(sv) != 1 {
		t.Fatalf("stale = %+v", sv)
	}
	if sv[0].Agent != "C" || sv[0].Branch != "agent-c" ||
		sv[0].Recommendation != "review" || len(sv[0].ConflictingFiles) != 1 ||
		sv[0].ConflictingFiles[0] != "pkg/billing/types.go" {
		t.Errorf("stale view = %+v", sv[0])
	}
	if sv[0].CreatedAt.IsZero() {
		t.Errorf("stale.created_at should be set")
	}
	if sv[0].Reason == "" || !strings.Contains(sv[0].Reason, "claimed scope") {
		t.Errorf("stale reason missing 'claimed scope': %q", sv[0].Reason)
	}

	// C "rebases past" the change. Use fast-forward merge of main into
	// agent-c since agent-c had no commits of its own: end result is the
	// same as a successful rebase — merge-base(agent-c, main) advances to
	// main's HEAD.
	gitInRepo(t, dir, "checkout", "-q", "agent-c")
	gitInRepo(t, dir, "merge", "--quiet", "--no-edit", "main")

	// Second refresh: stale should be cleared.
	runMustOK(t, "refresh")
	stdoutS = runMustOK(t, "stale", "--json")
	sv = nil
	if err := json.Unmarshal([]byte(stdoutS), &sv); err != nil {
		t.Fatalf("invalid stale JSON: %v\n%s", err, stdoutS)
	}
	if len(sv) != 0 {
		t.Errorf("stale should be empty after merge-up, got %+v", sv)
	}
}

func TestRefreshEditClaimRecommendsRebase(t *testing.T) {
	dir := newStaleScenarioRepo(t)
	t.Chdir(dir)
	runMustOK(t, "init")
	runMustOK(t, "agent", "add",
		"--name", "C", "--task", "edit claim variant",
		"--branch", "agent-c",
		"--claim", "glob:pkg/billing/**:edit",
	)
	stdout := runMustOK(t, "refresh", "--json")
	var rj struct {
		Refreshed []refreshResult `json:"refreshed"`
	}
	if err := json.Unmarshal([]byte(stdout), &rj); err != nil {
		t.Fatalf("invalid refresh JSON: %v\n%s", err, stdout)
	}
	if !rj.Refreshed[0].Stale {
		t.Fatalf("expected stale: %+v", rj.Refreshed[0])
	}
	if rj.Refreshed[0].Recommendation != "rebase" {
		t.Errorf("recommendation = %q, want rebase", rj.Refreshed[0].Recommendation)
	}
}

func TestRefreshNoOverlapNoStale(t *testing.T) {
	dir := newStaleScenarioRepo(t)
	t.Chdir(dir)
	runMustOK(t, "init")
	// Claim something unrelated to the file A modified.
	runMustOK(t, "agent", "add",
		"--name", "C", "--task", "no overlap",
		"--branch", "agent-c",
		"--claim", "glob:pkg/auth/**:edit",
	)
	stdout := runMustOK(t, "refresh", "--json")
	var rj struct {
		Refreshed []refreshResult `json:"refreshed"`
	}
	if err := json.Unmarshal([]byte(stdout), &rj); err != nil {
		t.Fatalf("invalid refresh JSON: %v\n%s", err, stdout)
	}
	if rj.Refreshed[0].Stale {
		t.Errorf("expected not stale: %+v", rj.Refreshed[0])
	}
	stdoutS := runMustOK(t, "stale", "--json")
	var sv []staleView
	if err := json.Unmarshal([]byte(stdoutS), &sv); err != nil {
		t.Fatalf("stale JSON: %v", err)
	}
	if len(sv) != 0 {
		t.Errorf("stale list should be empty: %+v", sv)
	}
}

func TestRefreshAgentFilter(t *testing.T) {
	dir := newStaleScenarioRepo(t)
	t.Chdir(dir)
	runMustOK(t, "init")
	runMustOK(t, "agent", "add",
		"--name", "C", "--task", "first",
		"--branch", "agent-c",
		"--claim", "glob:pkg/billing/**:edit",
	)
	// A second agent on the same branch with a non-overlapping claim.
	runMustOK(t, "agent", "add",
		"--name", "D", "--task", "second",
		"--branch", "agent-c",
		"--claim", "glob:pkg/auth/**:edit",
	)

	stdout := runMustOK(t, "refresh", "--agent", "C", "--json")
	var rj struct {
		Refreshed []refreshResult `json:"refreshed"`
	}
	if err := json.Unmarshal([]byte(stdout), &rj); err != nil {
		t.Fatalf("invalid refresh JSON: %v\n%s", err, stdout)
	}
	if len(rj.Refreshed) != 1 {
		t.Fatalf("expected exactly 1 refreshed agent, got %d: %+v", len(rj.Refreshed), rj.Refreshed)
	}
	if rj.Refreshed[0].Agent != "C" {
		t.Errorf("refreshed wrong agent: %q", rj.Refreshed[0].Agent)
	}
	if !rj.Refreshed[0].Stale {
		t.Errorf("C should be stale")
	}

	// D was not touched by --agent C; its stale mark (or lack thereof) stays
	// at whatever ReplaceStaleMarksForAgent has not been called for it. The
	// `stale` list should still contain only C.
	stdoutS := runMustOK(t, "stale", "--json")
	var sv []staleView
	if err := json.Unmarshal([]byte(stdoutS), &sv); err != nil {
		t.Fatal(err)
	}
	if len(sv) != 1 || sv[0].Agent != "C" {
		t.Errorf("stale should list only C, got %+v", sv)
	}
}

func TestRefreshUnknownAgentExitsOne(t *testing.T) {
	dir := newStaleScenarioRepo(t)
	t.Chdir(dir)
	runMustOK(t, "init")
	_, stderr, exit := runCLI(t, "refresh", "--agent", "ghost")
	if exit != 1 {
		t.Errorf("exit = %d, want 1", exit)
	}
	if !strings.Contains(stderr, "not found") {
		t.Errorf("stderr should mention not found: %s", stderr)
	}
}

func TestRefreshDeletedBaseBranchFails(t *testing.T) {
	dir := newStaleScenarioRepo(t)
	t.Chdir(dir)
	runMustOK(t, "init")
	runMustOK(t, "agent", "add",
		"--name", "C", "--task", "t",
		"--branch", "agent-c",
		"--claim", "glob:pkg/billing/**:edit",
	)
	// Switch off main so we can delete it.
	gitInRepo(t, dir, "checkout", "-q", "agent-c")
	gitInRepo(t, dir, "branch", "-D", "main")

	_, stderr, exit := runCLI(t, "refresh")
	if exit != 1 {
		t.Errorf("exit = %d, want 1", exit)
	}
	if !strings.Contains(stderr, "base branch") || !strings.Contains(stderr, "no longer exists") {
		t.Errorf("stderr should explain missing base branch: %s", stderr)
	}
}

func TestRefreshDeletedAgentBranchFails(t *testing.T) {
	dir := newStaleScenarioRepo(t)
	t.Chdir(dir)
	runMustOK(t, "init")
	runMustOK(t, "agent", "add",
		"--name", "C", "--task", "t",
		"--branch", "agent-c",
		"--claim", "glob:pkg/billing/**:edit",
	)
	gitInRepo(t, dir, "branch", "-D", "agent-c")

	_, stderr, exit := runCLI(t, "refresh")
	if exit != 1 {
		t.Errorf("exit = %d, want 1", exit)
	}
	if !strings.Contains(stderr, "branch") || !strings.Contains(stderr, "no longer exists") {
		t.Errorf("stderr should explain missing branch: %s", stderr)
	}
}

func TestStaleEmpty(t *testing.T) {
	dir := newStaleScenarioRepo(t)
	t.Chdir(dir)
	runMustOK(t, "init")
	stdout := runMustOK(t, "stale")
	if !strings.Contains(stdout, "no stale agents") {
		t.Errorf("expected 'no stale agents': %s", stdout)
	}
	stdout = runMustOK(t, "stale", "--json")
	stdout = strings.TrimSpace(stdout)
	// Either [] or null is acceptable; require the array form for stability.
	if stdout != "[]" {
		t.Errorf("stale --json should be [] when empty, got %q", stdout)
	}
}

func TestStaleJSONStableShape(t *testing.T) {
	dir := newStaleScenarioRepo(t)
	t.Chdir(dir)
	runMustOK(t, "init")
	runMustOK(t, "agent", "add",
		"--name", "C", "--task", "t",
		"--branch", "agent-c",
		"--claim", "glob:pkg/billing/**:edit",
	)
	runMustOK(t, "refresh")
	stdout := runMustOK(t, "stale", "--json")

	var raw []map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if len(raw) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(raw))
	}
	want := []string{"agent", "branch", "reason", "recommendation", "conflicting_files", "created_at"}
	for _, k := range want {
		if _, ok := raw[0][k]; !ok {
			t.Errorf("stale JSON missing key %q; got keys %v", k, mapKeys(raw[0]))
		}
	}
}

func mapKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// keep the import alive if other tests trim down later
var _ = fmt.Sprintf
