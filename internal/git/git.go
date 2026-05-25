package git

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var ErrNotARepo = errors.New("not inside a git repository (run 'git init' first)")

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
