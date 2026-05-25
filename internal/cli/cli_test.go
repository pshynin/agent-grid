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

// keep the import alive if other tests trim down later
var _ = fmt.Sprintf
