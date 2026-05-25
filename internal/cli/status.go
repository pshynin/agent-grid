package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/pshynin/agent-grid/internal/core"
	"github.com/pshynin/agent-grid/internal/git"
	"github.com/pshynin/agent-grid/internal/store"
)

func newStatusCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show one-line summary for every registered agent",
		Long: "For each agent, reports branch, commits ahead/behind base, the\n" +
			"latest persisted risk level, whether the agent is currently stale,\n" +
			"and whether its branch is merged into the base branch.\n" +
			"Run `agentgrid refresh` and `agentgrid diff-risk <agent>` to refresh\n" +
			"the underlying signals.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runStatus(cmd, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return cmd
}

type statusView struct {
	Name   string  `json:"name"`
	Branch string  `json:"branch"`
	Base   string  `json:"base"`
	Ahead  int     `json:"ahead"`
	Behind int     `json:"behind"`
	Risk   *string `json:"risk"`
	Stale  bool    `json:"stale"`
	Merged bool    `json:"merged"`
}

func runStatus(cmd *cobra.Command, jsonOut bool) error {
	cc, err := openCmdContext()
	if err != nil {
		return err
	}
	defer cc.Close()
	ctx := cmdCtx(cmd)

	agents, err := cc.Store.ListAgents(ctx)
	if err != nil {
		return err
	}

	staleMarks, err := cc.Store.ListStaleMarks(ctx)
	if err != nil {
		return err
	}
	staleByAgent := map[string]bool{}
	for _, m := range staleMarks {
		staleByAgent[m.AgentID] = true
	}

	views := make([]statusView, 0, len(agents))
	for _, a := range agents {
		v, err := buildStatusView(ctx, cc, a, staleByAgent[a.ID])
		if err != nil {
			return err
		}
		views = append(views, v)
	}

	if jsonOut {
		return writeJSON(cmd.OutOrStdout(), views)
	}
	renderStatus(cmd.OutOrStdout(), views)
	return nil
}

func buildStatusView(ctx context.Context, cc *cmdContext, a core.Agent, stale bool) (statusView, error) {
	v := statusView{Name: a.Name, Branch: a.Branch, Base: a.BaseBranch, Stale: stale}

	if ok, err := git.RefExists(cc.Repo, a.Branch); err != nil {
		return v, err
	} else if !ok {
		return v, fmt.Errorf("agent %q: branch %q no longer exists", a.Name, a.Branch)
	}
	if ok, err := git.RefExists(cc.Repo, a.BaseBranch); err != nil {
		return v, err
	} else if !ok {
		return v, fmt.Errorf("agent %q: base branch %q no longer exists", a.Name, a.BaseBranch)
	}

	ab, err := git.AheadBehind(cc.Repo, a.BaseBranch, a.Branch)
	if err != nil {
		return v, fmt.Errorf("agent %q: %w", a.Name, err)
	}
	v.Ahead, v.Behind = ab.Ahead, ab.Behind

	branchHead, err := git.CurrentHead(cc.Repo, a.Branch)
	if err != nil {
		return v, fmt.Errorf("agent %q: %w", a.Name, err)
	}
	baseHead, err := git.CurrentHead(cc.Repo, a.BaseBranch)
	if err != nil {
		return v, fmt.Errorf("agent %q: %w", a.Name, err)
	}
	merged, err := git.IsAncestor(cc.Repo, branchHead, baseHead)
	if err != nil {
		return v, fmt.Errorf("agent %q: %w", a.Name, err)
	}
	v.Merged = merged

	snap, err := cc.Store.LatestDiffSnapshotByAgent(ctx, a.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return v, err
	}
	if err == nil {
		level := string(snap.RiskLevel)
		v.Risk = &level
	}
	return v, nil
}

func renderStatus(w io.Writer, views []statusView) {
	if len(views) == 0 {
		fmt.Fprintln(w, "no agents registered.")
		return
	}
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "NAME\tBRANCH\tBASE\tAHEAD/BEHIND\tRISK\tSTALE\tMERGED")
	for _, v := range views {
		risk := "-"
		if v.Risk != nil {
			risk = *v.Risk
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d/%d\t%s\t%s\t%s\n",
			v.Name, v.Branch, v.Base, v.Ahead, v.Behind, risk,
			yesNo(v.Stale), yesNo(v.Merged))
	}
	_ = tw.Flush()
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
