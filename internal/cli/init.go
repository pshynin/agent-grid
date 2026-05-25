package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/pshynin/agent-grid/internal/git"
	"github.com/pshynin/agent-grid/internal/store"
)

func newInitCmd() *cobra.Command {
	var baseBranch string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize AgentGrid in the current git repository",
		Long: "Create .agentgrid/ with a SQLite state database. Idempotent: " +
			"re-running on an initialized repo applies any new migrations and " +
			"prints a summary.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("getwd: %w", err)
			}
			root, err := git.RepoRoot(cwd)
			if err != nil {
				return err
			}

			agDir := filepath.Join(root, ".agentgrid")
			if err := os.MkdirAll(agDir, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", agDir, err)
			}

			dbPath := filepath.Join(agDir, "state.db")
			dbExisted := fileExists(dbPath)

			s, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer s.Close()

			out := cmd.OutOrStdout()
			if dbExisted {
				fmt.Fprintln(out, "agentgrid already initialized; migrations up to date")
			} else {
				fmt.Fprintln(out, "initialized .agentgrid/")
			}
			fmt.Fprintf(out, "  repo:     %s\n", root)
			fmt.Fprintf(out, "  database: %s\n", relTo(root, dbPath))
			fmt.Fprintf(out, "  base:     %s\n", baseBranch)
			return nil
		},
	}
	cmd.Flags().StringVar(&baseBranch, "base-branch", "main", "default base branch")
	return cmd
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func relTo(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return rel
}
