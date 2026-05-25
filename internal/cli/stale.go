package cli

import (
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/pshynin/agent-grid/internal/core"
)

func newStaleCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "stale",
		Short: "Show agents marked stale by the most recent refresh",
		Long: "Lists the current stale_marks: one entry per agent that overlapped\n" +
			"the base branch's recent changes. Run `agentgrid refresh` to update.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStale(cmd, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return cmd
}

type staleView struct {
	Agent            string    `json:"agent"`
	Branch           string    `json:"branch"`
	Reason           string    `json:"reason"`
	Recommendation   string    `json:"recommendation"`
	ConflictingFiles []string  `json:"conflicting_files"`
	CreatedAt        time.Time `json:"created_at"`
}

func runStale(cmd *cobra.Command, jsonOut bool) error {
	cc, err := openCmdContext()
	if err != nil {
		return err
	}
	defer cc.Close()
	ctx := cmdCtx(cmd)

	marks, err := cc.Store.ListStaleMarks(ctx)
	if err != nil {
		return err
	}
	agents, err := cc.Store.ListAgents(ctx)
	if err != nil {
		return err
	}
	byID := make(map[string]core.Agent, len(agents))
	for _, a := range agents {
		byID[a.ID] = a
	}

	views := make([]staleView, 0, len(marks))
	for _, m := range marks {
		a := byID[m.AgentID]
		views = append(views, staleView{
			Agent:            a.Name,
			Branch:           a.Branch,
			Reason:           m.Reason,
			Recommendation:   string(m.Recommendation),
			ConflictingFiles: m.ConflictingFiles,
			CreatedAt:        m.CreatedAt,
		})
	}

	if jsonOut {
		return writeJSON(cmd.OutOrStdout(), views)
	}
	renderStale(cmd.OutOrStdout(), views)
	return nil
}

func renderStale(w io.Writer, views []staleView) {
	if len(views) == 0 {
		fmt.Fprintln(w, "no stale agents.")
		return
	}
	for i, v := range views {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "agent: %s\n", v.Agent)
		fmt.Fprintf(w, "  branch:         %s\n", v.Branch)
		fmt.Fprintf(w, "  recommendation: %s\n", v.Recommendation)
		fmt.Fprintf(w, "  reason:         %s\n", v.Reason)
		if len(v.ConflictingFiles) > 0 {
			fmt.Fprintln(w, "  files:")
			for _, f := range v.ConflictingFiles {
				fmt.Fprintf(w, "    %s\n", f)
			}
		}
	}
}
