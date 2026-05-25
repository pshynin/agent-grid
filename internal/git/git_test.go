package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRun runs a git command in dir and fails the test on non-zero exit.
func gitRun(t *testing.T, dir string, args ...string) string {
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
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newSimpleRepo creates main (one commit) + feat/work (one extra commit), and
// checks main out. Used by the basic test cases.
func newSimpleRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	writeFile(t, dir, "a.txt", "a\n")
	gitRun(t, dir, "add", "a.txt")
	gitRun(t, dir, "commit", "-q", "-m", "init")
	gitRun(t, dir, "checkout", "-q", "-b", "feat/work")
	writeFile(t, dir, "b.txt", "b\n")
	gitRun(t, dir, "add", "b.txt")
	gitRun(t, dir, "commit", "-q", "-m", "feat work")
	gitRun(t, dir, "checkout", "-q", "main")
	return dir
}

// newRichRepo creates a more complex repo with:
//   - main: initial commit + a second commit so feat is behind by 1
//   - feat/work: branched from main's first commit; modifies a file, renames
//     another, adds a binary file
func newRichRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q", "-b", "main")
	writeFile(t, dir, "keep.txt", "kept\n")
	writeFile(t, dir, "to_rename.txt", "one\ntwo\nthree\n")
	gitRun(t, dir, "add", ".")
	gitRun(t, dir, "commit", "-q", "-m", "init")

	// feat/work branches off the first commit.
	gitRun(t, dir, "checkout", "-q", "-b", "feat/work")
	writeFile(t, dir, "keep.txt", "kept\nmore\n") // modify
	gitRun(t, dir, "add", "keep.txt")
	gitRun(t, dir, "commit", "-q", "-m", "modify keep")

	// Rename + small edit in the same commit.
	gitRun(t, dir, "mv", "to_rename.txt", "renamed.txt")
	writeFile(t, dir, "renamed.txt", "one\ntwo\nthree\nfour\n")
	gitRun(t, dir, "add", "renamed.txt")
	gitRun(t, dir, "commit", "-q", "-m", "rename with edit")

	// Add a binary file.
	if err := os.WriteFile(filepath.Join(dir, "image.bin"),
		[]byte{0, 1, 2, 3, 0, 5, 6, 7, 8, 9, 0, 11}, 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "image.bin")
	gitRun(t, dir, "commit", "-q", "-m", "add binary")

	// Move main forward by one commit so feat/work is behind by 1.
	gitRun(t, dir, "checkout", "-q", "main")
	writeFile(t, dir, "main_only.txt", "main\n")
	gitRun(t, dir, "add", "main_only.txt")
	gitRun(t, dir, "commit", "-q", "-m", "main moves on")

	return dir
}

// -------------------------------------------------- existing helpers (sanity) --

func TestRepoRoot(t *testing.T) {
	dir := newSimpleRepo(t)
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
	dir := newSimpleRepo(t)
	for _, ref := range []string{"main", "feat/work", "HEAD"} {
		ok, err := RefExists(dir, ref)
		if err != nil || !ok {
			t.Errorf("RefExists(%q) = (%v, %v), want (true, nil)", ref, ok, err)
		}
	}
	ok, err := RefExists(dir, "does-not-exist")
	if err != nil || ok {
		t.Errorf("RefExists(unknown) = (%v, %v), want (false, nil)", ok, err)
	}
}

// ---------------------------------------------------------------- CurrentHead --

func TestCurrentHead(t *testing.T) {
	dir := newSimpleRepo(t)
	want := gitRun(t, dir, "rev-parse", "main")
	got, err := CurrentHead(dir, "main")
	if err != nil {
		t.Fatalf("CurrentHead: %v", err)
	}
	if got != want {
		t.Errorf("CurrentHead(main) = %q, want %q", got, want)
	}
}

func TestCurrentHeadUnknownRef(t *testing.T) {
	dir := newSimpleRepo(t)
	_, err := CurrentHead(dir, "no-such-branch")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ------------------------------------------------------------------ MergeBase --

func TestMergeBase(t *testing.T) {
	dir := newSimpleRepo(t)
	sha, err := MergeBase(dir, "feat/work", "main")
	if err != nil {
		t.Fatalf("MergeBase: %v", err)
	}
	if len(sha) != 40 {
		t.Errorf("sha = %q (len %d), want 40-char hex", sha, len(sha))
	}
	want := gitRun(t, dir, "rev-parse", "main")
	if sha != want {
		t.Errorf("merge-base = %q, main HEAD = %q", sha, want)
	}
}

func TestMergeBaseUnknownRef(t *testing.T) {
	dir := newSimpleRepo(t)
	_, err := MergeBase(dir, "feat/work", "no-such-branch")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ----------------------------------------------------------------- IsAncestor --

func TestIsAncestor(t *testing.T) {
	dir := newSimpleRepo(t)
	mainSHA := gitRun(t, dir, "rev-parse", "main")

	// main is ancestor of feat/work (feat/work branched off main).
	ok, err := IsAncestor(dir, mainSHA, "feat/work")
	if err != nil || !ok {
		t.Errorf("IsAncestor(main, feat/work) = (%v, %v), want (true, nil)", ok, err)
	}
	// feat/work is NOT ancestor of main.
	ok, err = IsAncestor(dir, "feat/work", "main")
	if err != nil || ok {
		t.Errorf("IsAncestor(feat/work, main) = (%v, %v), want (false, nil)", ok, err)
	}
	// A ref is its own ancestor.
	ok, err = IsAncestor(dir, "main", "main")
	if err != nil || !ok {
		t.Errorf("IsAncestor(main, main) = (%v, %v), want (true, nil)", ok, err)
	}
}

func TestIsAncestorUnknownRef(t *testing.T) {
	dir := newSimpleRepo(t)
	_, err := IsAncestor(dir, "main", "no-such-ref")
	if err == nil {
		t.Fatal("expected error for unknown ref")
	}
}

// ---------------------------------------------------------------- AheadBehind --

func TestAheadBehind(t *testing.T) {
	dir := newRichRepo(t)
	// feat/work is 3 commits ahead of the merge-base, main is 1 commit
	// ahead of the merge-base. So:
	//   ahead(main, feat/work) = 3 (feat/work has 3 commits main doesn't)
	//   behind(main, feat/work) = 1 (main has 1 commit feat/work doesn't)
	got, err := AheadBehind(dir, "main", "feat/work")
	if err != nil {
		t.Fatalf("AheadBehind: %v", err)
	}
	if got.Ahead != 3 || got.Behind != 1 {
		t.Errorf("AheadBehind(main, feat/work) = %+v, want {Ahead:3, Behind:1}", got)
	}

	// Same branch should be zero in both directions.
	got, err = AheadBehind(dir, "main", "main")
	if err != nil {
		t.Fatalf("AheadBehind(main,main): %v", err)
	}
	if got.Ahead != 0 || got.Behind != 0 {
		t.Errorf("AheadBehind(main,main) = %+v, want zero", got)
	}
}

func TestAheadBehindUnknownRef(t *testing.T) {
	dir := newSimpleRepo(t)
	_, err := AheadBehind(dir, "main", "no-such-ref")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --------------------------------------------------------------- DiffNameOnly --

func TestDiffNameOnly(t *testing.T) {
	dir := newRichRepo(t)
	paths, err := DiffNameOnly(dir, "main", "feat/work")
	if err != nil {
		t.Fatalf("DiffNameOnly: %v", err)
	}
	got := strSet(paths)
	// No -M flag, so the rename shows as delete+add.
	for _, want := range []string{"keep.txt", "to_rename.txt", "renamed.txt", "image.bin"} {
		if !got[want] {
			t.Errorf("DiffNameOnly missing %q (got %v)", want, paths)
		}
	}
	// main_only.txt is on main but not feat/work; it appears as a delete in the diff.
	if !got["main_only.txt"] {
		t.Errorf("DiffNameOnly should include main_only.txt (delete), got %v", paths)
	}
}

func TestDiffNameOnlyEmpty(t *testing.T) {
	dir := newSimpleRepo(t)
	paths, err := DiffNameOnly(dir, "main", "main")
	if err != nil {
		t.Fatalf("DiffNameOnly: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("DiffNameOnly(main,main) = %v, want empty", paths)
	}
}

func TestDiffNameOnlyUnknownRef(t *testing.T) {
	dir := newSimpleRepo(t)
	_, err := DiffNameOnly(dir, "main", "no-such-ref")
	if err == nil {
		t.Fatal("expected error")
	}
}

// ----------------------------------------------------------------- DiffNumstat --

func TestDiffNumstatDetectsRenameAndBinary(t *testing.T) {
	dir := newRichRepo(t)
	entries, err := DiffNumstat(dir, "main", "feat/work")
	if err != nil {
		t.Fatalf("DiffNumstat: %v", err)
	}
	byPath := map[string]NumstatEntry{}
	for _, e := range entries {
		byPath[e.Path] = e
	}

	keep, ok := byPath["keep.txt"]
	if !ok {
		t.Errorf("missing keep.txt entry; got %+v", entries)
	} else if keep.Added != 1 || keep.Removed != 0 || keep.Binary {
		t.Errorf("keep.txt entry = %+v, want Added:1 Removed:0 Binary:false", keep)
	}

	// Rename detection with -M should produce a single entry whose Path is
	// the new name and OldPath the previous one.
	renamed, ok := byPath["renamed.txt"]
	if !ok {
		t.Errorf("missing renamed.txt entry; got %+v", entries)
	} else if renamed.OldPath != "to_rename.txt" {
		t.Errorf("renamed.txt OldPath = %q, want to_rename.txt (entry=%+v)", renamed.OldPath, renamed)
	}
	if _, present := byPath["to_rename.txt"]; present {
		t.Errorf("to_rename.txt should be absent under -M rename detection (entry: %+v)", byPath["to_rename.txt"])
	}

	bin, ok := byPath["image.bin"]
	if !ok {
		t.Errorf("missing image.bin entry; got %+v", entries)
	} else if !bin.Binary || bin.Added != 0 || bin.Removed != 0 {
		t.Errorf("image.bin entry = %+v, want Binary:true Added:0 Removed:0", bin)
	}
}

func TestDiffNumstatEmpty(t *testing.T) {
	dir := newSimpleRepo(t)
	entries, err := DiffNumstat(dir, "main", "main")
	if err != nil {
		t.Fatalf("DiffNumstat: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("DiffNumstat(main,main) = %+v, want empty", entries)
	}
}

func TestDiffNumstatUnknownRef(t *testing.T) {
	dir := newSimpleRepo(t)
	_, err := DiffNumstat(dir, "main", "no-such-ref")
	if err == nil {
		t.Fatal("expected error")
	}
}

// --------------------------------------------------- parseNumstat unit tests --

func TestParseNumstatMalformedInputs(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"missing tabs", []byte("xxxx\x00")},
		{"truncated rename", []byte("3\t1\t\x00onlyOld")},
		{"non-numeric added", []byte("abc\t1\tpath\x00")},
		{"non-numeric removed", []byte("1\tabc\tpath\x00")},
		{"inconsistent binary fields", []byte("-\t5\tpath\x00")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseNumstat(tc.data); err == nil {
				t.Errorf("parseNumstat(%q) returned nil error", tc.data)
			}
		})
	}
}

func TestParseNumstatRoundTrip(t *testing.T) {
	data := []byte("3\t1\tkeep.txt\x000\t0\trenamed.txt\x00")
	// Add a synthetic rename: "added\tremoved\t\0old\0new\0"
	data = append(data, []byte("4\t2\t\x00to_rename.txt\x00renamed_actually.txt\x00")...)
	entries, err := parseNumstat(data)
	if err != nil {
		t.Fatalf("parseNumstat: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(entries), entries)
	}
	if entries[0].Path != "keep.txt" || entries[0].Added != 3 || entries[0].Removed != 1 {
		t.Errorf("entry[0] = %+v", entries[0])
	}
	if entries[2].OldPath != "to_rename.txt" || entries[2].Path != "renamed_actually.txt" {
		t.Errorf("entry[2] = %+v", entries[2])
	}
}

func strSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}
