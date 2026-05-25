package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/pshynin/agent-grid/internal/core"
	"github.com/pshynin/agent-grid/internal/git"
	"github.com/pshynin/agent-grid/internal/policy"
	"github.com/pshynin/agent-grid/internal/store"
)

func newRefreshCmd() *cobra.Command {
	var (
		agentName string
		jsonOut   bool
	)
	cmd := &cobra.Command{
		Use:   "refresh",
		Short: "Recompute stale marks for one or all agents",
		Long: "Walks each registered agent, diffs its base branch against the live\n" +
			"merge-base with the agent's branch, and writes (or clears) a stale\n" +
			"mark depending on whether the changed files overlap the agent's\n" +
			"claims.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runRefresh(cmd, agentName, jsonOut)
		},
	}
	cmd.Flags().StringVar(&agentName, "agent", "", "limit refresh to this agent")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return cmd
}

type refreshResult struct {
	Agent            string              `json:"agent"`
	Branch           string              `json:"branch"`
	Stale            bool                `json:"stale"`
	ConflictingFiles []string            `json:"conflicting_files,omitempty"`
	Reason           string              `json:"reason,omitempty"`
	Recommendation   core.Recommendation `json:"recommendation,omitempty"`
}

func runRefresh(cmd *cobra.Command, agentName string, jsonOut bool) error {
	cc, err := openCmdContext()
	if err != nil {
		return err
	}
	defer cc.Close()
	ctx := cmdCtx(cmd)

	var agents []core.Agent
	if agentName != "" {
		a, err := cc.Store.GetAgentByName(ctx, agentName)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("agent %q not found", agentName)
			}
			return err
		}
		agents = []core.Agent{a}
	} else {
		agents, err = cc.Store.ListAgents(ctx)
		if err != nil {
			return err
		}
	}

	results := make([]refreshResult, 0, len(agents))
	for _, a := range agents {
		res, err := refreshOne(ctx, cc, a)
		if err != nil {
			return err
		}
		results = append(results, res)
	}

	if jsonOut {
		return writeJSON(cmd.OutOrStdout(), map[string]any{"refreshed": results})
	}
	renderRefresh(cmd.OutOrStdout(), results)
	return nil
}

func refreshOne(ctx context.Context, cc *cmdContext, a core.Agent) (refreshResult, error) {
	res := refreshResult{Agent: a.Name, Branch: a.Branch}

	if ok, err := git.RefExists(cc.Repo, a.Branch); err != nil {
		return res, err
	} else if !ok {
		return res, fmt.Errorf("agent %q: branch %q no longer exists", a.Name, a.Branch)
	}
	if ok, err := git.RefExists(cc.Repo, a.BaseBranch); err != nil {
		return res, err
	} else if !ok {
		return res, fmt.Errorf("agent %q: base branch %q no longer exists", a.Name, a.BaseBranch)
	}

	// Effective base = current merge-base(branch, base_branch). The stored
	// agent.BaseCommit is the registration-time snapshot; using merge-base
	// at refresh time ensures that an agent which rebased past a conflicting
	// commit correctly clears its stale mark.
	effectiveBase, err := git.MergeBase(cc.Repo, a.Branch, a.BaseBranch)
	if err != nil {
		return res, fmt.Errorf("agent %q: %w", a.Name, err)
	}
	baseHead, err := git.CurrentHead(cc.Repo, a.BaseBranch)
	if err != nil {
		return res, fmt.Errorf("agent %q: %w", a.Name, err)
	}

	var changed []string
	if effectiveBase != baseHead {
		changed, err = git.DiffNameOnly(cc.Repo, effectiveBase, baseHead)
		if err != nil {
			return res, fmt.Errorf("agent %q: %w", a.Name, err)
		}
	}

	claims, err := cc.Store.ListClaimsByAgent(ctx, a.ID)
	if err != nil {
		return res, fmt.Errorf("agent %q: %w", a.Name, err)
	}

	mark, isStale := policy.DeriveStaleMark(a.ID, claims, changed)
	var marks []core.StaleMark
	if isStale {
		mark.ID = uuid.NewString()
		mark.CreatedAt = time.Now().UTC()
		marks = []core.StaleMark{mark}
	}
	if err := cc.Store.ReplaceStaleMarksForAgent(ctx, a.ID, marks); err != nil {
		return res, fmt.Errorf("agent %q: %w", a.Name, err)
	}

	res.Stale = isStale
	if isStale {
		res.ConflictingFiles = mark.ConflictingFiles
		res.Reason = mark.Reason
		res.Recommendation = mark.Recommendation
	}
	return res, nil
}

func renderRefresh(w io.Writer, results []refreshResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, "no agents registered.")
		return
	}
	stale := 0
	for _, r := range results {
		if r.Stale {
			stale++
		}
	}
	noun := "agent"
	if len(results) != 1 {
		noun = "agents"
	}
	fmt.Fprintf(w, "refreshed %d %s, %d stale\n", len(results), noun, stale)
	for _, r := range results {
		if r.Stale {
			plural := "file"
			if len(r.ConflictingFiles) != 1 {
				plural = "files"
			}
			fmt.Fprintf(w, "  %s: stale (%s) — %d %s\n",
				r.Agent, r.Recommendation, len(r.ConflictingFiles), plural)
		} else {
			fmt.Fprintf(w, "  %s: clean\n", r.Agent)
		}
	}
}
