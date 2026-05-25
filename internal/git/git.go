// Package git is a thin adapter over the `git` CLI. It shells out for every
// operation and parses porcelain or `-z` machine-readable output. It does not
// load object databases or read .git internals directly.
package git

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// ErrNotARepo is returned by RepoRoot when cwd is not inside a git repository.
var ErrNotARepo = errors.New("not inside a git repository (run 'git init' first)")

// NumstatEntry describes one file's diff statistics. For renamed files,
// OldPath is set to the previous name and Path is the new name. Binary files
// have Binary=true and Added/Removed=0.
type NumstatEntry struct {
	Path    string
	OldPath string
	Added   int
	Removed int
	Binary  bool
}

// AheadBehindCount is the result of comparing two refs via the symmetric
// difference. Ahead is the number of commits in head not in base; Behind is
// the number of commits in base not in head.
type AheadBehindCount struct {
	Ahead  int
	Behind int
}

// RepoRoot returns the absolute path of the git working tree containing cwd.
func RepoRoot(cwd string) (string, error) {
	out, err := runGit(cwd, "rev-parse", "--show-toplevel")
	if err != nil {
		if isNotARepoError(err) {
			return "", ErrNotARepo
		}
		return "", err
	}
	return out, nil
}

// RefExists reports whether the given ref (branch, tag, commit) resolves to a
// commit in repoRoot. Missing refs return (false, nil); only unexpected
// failures return a non-nil error.
func RefExists(repoRoot, ref string) (bool, error) {
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if exitCodeIs(err, 1) {
		return false, nil
	}
	return false, gitError("rev-parse "+ref, err, &stderr)
}

// CurrentHead returns the commit SHA that ref points at. Tags are peeled to
// the underlying commit. Missing refs return a typed error.
func CurrentHead(repoRoot, ref string) (string, error) {
	return runGit(repoRoot, "rev-parse", "--verify", ref+"^{commit}")
}

// MergeBase returns the merge-base commit of ref1 and ref2.
func MergeBase(repoRoot, ref1, ref2 string) (string, error) {
	sha, err := runGit(repoRoot, "merge-base", ref1, ref2)
	if err != nil {
		return "", err
	}
	if sha == "" {
		return "", fmt.Errorf("git merge-base %s %s: no common ancestor", ref1, ref2)
	}
	return sha, nil
}

// IsAncestor reports whether ancestor is an ancestor commit of descendant
// (equivalently: descendant contains ancestor in its history). The two refs
// being identical also returns true.
func IsAncestor(repoRoot, ancestor, descendant string) (bool, error) {
	cmd := exec.Command("git", "-C", repoRoot, "merge-base", "--is-ancestor", ancestor, descendant)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	if exitCodeIs(err, 1) {
		return false, nil
	}
	return false, gitError(fmt.Sprintf("merge-base --is-ancestor %s %s", ancestor, descendant), err, &stderr)
}

// AheadBehind returns commit counts between base and head. The result is
// reported from the perspective of head: Ahead is commits on head not on
// base; Behind is commits on base not on head.
func AheadBehind(repoRoot, base, head string) (AheadBehindCount, error) {
	out, err := runGit(repoRoot, "rev-list", "--left-right", "--count", base+"..."+head)
	if err != nil {
		return AheadBehindCount{}, err
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return AheadBehindCount{}, fmt.Errorf("rev-list --left-right %s...%s: unexpected output %q", base, head, out)
	}
	behind, err := strconv.Atoi(fields[0])
	if err != nil {
		return AheadBehindCount{}, fmt.Errorf("rev-list: parse behind %q: %w", fields[0], err)
	}
	ahead, err := strconv.Atoi(fields[1])
	if err != nil {
		return AheadBehindCount{}, fmt.Errorf("rev-list: parse ahead %q: %w", fields[1], err)
	}
	return AheadBehindCount{Ahead: ahead, Behind: behind}, nil
}

// DiffNameOnly returns the set of paths changed between base and head.
// Rename detection is deliberately disabled (`--no-renames`) so a renamed
// file appears as both a delete (old name) and an add (new name). Stale
// detection wants the union of touched paths; use DiffNumstat when rename
// information matters.
func DiffNameOnly(repoRoot, base, head string) ([]string, error) {
	data, err := runGitBytes(repoRoot, "diff", "--name-only", "-z", "--no-renames", base, head)
	if err != nil {
		return nil, err
	}
	return splitZ(data), nil
}

// DiffNumstat returns one NumstatEntry per file changed between base and
// head. Rename detection is enabled (`-M`), so a renamed file produces one
// entry with OldPath set; the surrounding lines/added/removed counts reflect
// content changes after the rename.
func DiffNumstat(repoRoot, base, head string) ([]NumstatEntry, error) {
	data, err := runGitBytes(repoRoot, "diff", "--numstat", "-z", "-M", base, head)
	if err != nil {
		return nil, err
	}
	return parseNumstat(data)
}

// --- internal helpers -----------------------------------------------------

func runGit(repoRoot string, args ...string) (string, error) {
	data, err := runGitBytes(repoRoot, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func runGitBytes(repoRoot string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, gitError(strings.Join(args, " "), err, &stderr)
	}
	return stdout.Bytes(), nil
}

func gitError(op string, err error, stderr *bytes.Buffer) error {
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		return fmt.Errorf("git %s: %w", op, err)
	}
	return fmt.Errorf("git %s: %w (%s)", op, err, msg)
}

func exitCodeIs(err error, code int) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == code
}

func isNotARepoError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not a git repository")
}

func splitZ(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	parts := bytes.Split(data, []byte{0})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if len(p) == 0 {
			continue
		}
		out = append(out, string(p))
	}
	return out
}

func parseNumstat(data []byte) ([]NumstatEntry, error) {
	if len(data) == 0 {
		return nil, nil
	}
	parts := bytes.Split(data, []byte{0})
	out := make([]NumstatEntry, 0, len(parts))
	i := 0
	for i < len(parts) {
		raw := parts[i]
		if len(raw) == 0 {
			i++
			continue
		}
		fields := bytes.SplitN(raw, []byte{'\t'}, 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf("numstat: malformed entry %q", string(raw))
		}
		var entry NumstatEntry
		switch {
		case bytes.Equal(fields[0], []byte{'-'}) && bytes.Equal(fields[1], []byte{'-'}):
			entry.Binary = true
		case bytes.Equal(fields[0], []byte{'-'}) || bytes.Equal(fields[1], []byte{'-'}):
			return nil, fmt.Errorf("numstat: inconsistent binary fields in %q", string(raw))
		default:
			a, err := strconv.Atoi(string(fields[0]))
			if err != nil {
				return nil, fmt.Errorf("numstat added %q: %w", fields[0], err)
			}
			r, err := strconv.Atoi(string(fields[1]))
			if err != nil {
				return nil, fmt.Errorf("numstat removed %q: %w", fields[1], err)
			}
			entry.Added = a
			entry.Removed = r
		}

		path := string(fields[2])
		if path == "" {
			// Rename: next two NUL-separated tokens are old and new paths.
			if i+2 >= len(parts) {
				return nil, fmt.Errorf("numstat: rename entry truncated near %q", string(raw))
			}
			entry.OldPath = string(parts[i+1])
			entry.Path = string(parts[i+2])
			i += 3
		} else {
			entry.Path = path
			i++
		}
		out = append(out, entry)
	}
	return out, nil
}
