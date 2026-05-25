package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
	run("checkout", "-q", "-b", "feat/work")
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "b.txt")
	run("commit", "-q", "-m", "feat work")
	run("checkout", "-q", "main")
	return dir
}

func TestRepoRoot(t *testing.T) {
	dir := newTestRepo(t)
	got, err := RepoRoot(dir)
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	resolved, _ := filepath.EvalSymlinks(dir)
	if filepath.Clean(got) != filepath.Clean(resolved) && filepath.Clean(got) != filepath.Clean(dir) {
		t.Errorf("RepoRoot = %q, want %q (or %q)", got, dir, resolved)
	}
}

func TestRepoRootNotARepo(t *testing.T) {
	_, err := RepoRoot(t.TempDir())
	if err == nil {
		t.Fatal("expected error for non-repo directory")
	}
}

func TestRefExists(t *testing.T) {
	dir := newTestRepo(t)
	for _, ref := range []string{"main", "feat/work", "HEAD"} {
		ok, err := RefExists(dir, ref)
		if err != nil {
			t.Errorf("RefExists(%q) error: %v", ref, err)
		}
		if !ok {
			t.Errorf("RefExists(%q) = false, want true", ref)
		}
	}
	ok, err := RefExists(dir, "does-not-exist")
	if err != nil {
		t.Errorf("RefExists(unknown) error: %v", err)
	}
	if ok {
		t.Error("RefExists(unknown) = true")
	}
}

func TestMergeBase(t *testing.T) {
	dir := newTestRepo(t)
	sha, err := MergeBase(dir, "feat/work", "main")
	if err != nil {
		t.Fatalf("MergeBase: %v", err)
	}
	if len(sha) != 40 {
		t.Errorf("MergeBase sha = %q (len %d), want 40-char hex", sha, len(sha))
	}

	mainHead, err := exec.Command("git", "-C", dir, "rev-parse", "main").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(mainHead)) != sha {
		t.Errorf("merge-base %q != main HEAD %q", sha, strings.TrimSpace(string(mainHead)))
	}
}

func TestMergeBaseUnknownRef(t *testing.T) {
	dir := newTestRepo(t)
	_, err := MergeBase(dir, "feat/work", "no-such-branch")
	if err == nil {
		t.Fatal("expected error")
	}
}
