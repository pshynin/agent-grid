package git

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var ErrNotARepo = errors.New("not inside a git repository (run 'git init' first)")

// RepoRoot returns the absolute path of the git working tree containing cwd.
func RepoRoot(cwd string) (string, error) {
	cmd := exec.Command("git", "-C", cwd, "rev-parse", "--show-toplevel")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(stderr.String(), "not a git repository") {
			return "", ErrNotARepo
		}
		return "", fmt.Errorf("git rev-parse: %w (%s)", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// RefExists reports whether the given ref (branch, tag, commit) resolves in
// repoRoot. It returns (false, nil) when the ref simply does not exist, and a
// non-nil error only for unexpected failures.
func RefExists(repoRoot, ref string) (bool, error) {
	cmd := exec.Command("git", "-C", repoRoot, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// `git rev-parse --verify --quiet` exits 1 when the ref is absent.
			return false, nil
		}
		return false, fmt.Errorf("git rev-parse %s: %w (%s)", ref, err, strings.TrimSpace(stderr.String()))
	}
	return true, nil
}

// MergeBase returns the merge-base commit sha of ref1 and ref2.
func MergeBase(repoRoot, ref1, ref2 string) (string, error) {
	cmd := exec.Command("git", "-C", repoRoot, "merge-base", ref1, ref2)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git merge-base %s %s: %w (%s)",
			ref1, ref2, err, strings.TrimSpace(stderr.String()))
	}
	sha := strings.TrimSpace(stdout.String())
	if sha == "" {
		return "", fmt.Errorf("git merge-base %s %s: no common ancestor", ref1, ref2)
	}
	return sha, nil
}
