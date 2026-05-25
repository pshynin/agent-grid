package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/pshynin/agent-grid/internal/config"
	"github.com/pshynin/agent-grid/internal/git"
	"github.com/pshynin/agent-grid/internal/store"
)

func newInitCmd() *cobra.Command {
	var baseBranch string
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize AgentGrid in the current git repository",
		Long: "Create .agentgrid/ with a default config.yaml and a SQLite\n" +
			"state database. Idempotent: re-running on an initialized repo\n" +
			"applies any new migrations, validates the existing config, and\n" +
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

			cfgPath := config.Path(root)
			cfgExisted := fileExists(cfgPath)
			if !cfgExisted {
				if err := config.WriteDefault(cfgPath, baseBranch); err != nil {
					return err
				}
			}

			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}

			dbPath := filepath.Join(agDir, "state.db")
			dbExisted := fileExists(dbPath)

			s, err := store.Open(dbPath)
			if err != nil {
				return err
			}
			defer s.Close()

			out := cmd.OutOrStdout()
			switch {
			case !cfgExisted && !dbExisted:
				fmt.Fprintln(out, "initialized .agentgrid/")
			case cfgExisted && dbExisted:
				fmt.Fprintln(out, "agentgrid already initialized; config valid, migrations up to date")
			default:
				fmt.Fprintln(out, "agentgrid initialization completed")
			}
			fmt.Fprintf(out, "  repo:     %s\n", root)
			fmt.Fprintf(out, "  config:   %s%s\n", relTo(root, cfgPath), tag(cfgExisted, " (existing)"))
			fmt.Fprintf(out, "  database: %s%s\n", relTo(root, dbPath), tag(dbExisted, " (existing)"))
			fmt.Fprintf(out, "  base:     %s\n", cfg.DefaultBaseBranch)
			return nil
		},
	}
	cmd.Flags().StringVar(&baseBranch, "base-branch", "main",
		"default base branch written into a new config.yaml; ignored if config already exists")
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

func tag(b bool, s string) string {
	if b {
		return s
	}
	return ""
}
