package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/pshynin/agent-grid/internal/core"
	"github.com/pshynin/agent-grid/internal/git"
	"github.com/pshynin/agent-grid/internal/policy"
	"github.com/pshynin/agent-grid/internal/store"
)

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Manage agents",
	}
	cmd.AddCommand(newAgentAddCmd(), newAgentListCmd(), newAgentShowCmd())
	return cmd
}

func newAgentAddCmd() *cobra.Command {
	var (
		name, task, branch, base, worktree string
		claimSpecs                         []string
		jsonOut                            bool
	)
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Register a new agent (and optionally its initial claims)",
		Long: "Registers an agent against a pre-existing branch. The branch and base branch\n" +
			"must already exist in the working git repository. AgentGrid does not create\n" +
			"branches or worktrees in v0.1.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAgentAdd(cmd, agentAddOpts{
				Name: name, Task: task, Branch: branch, Base: base,
				Worktree: worktree, ClaimSpecs: claimSpecs, JSON: jsonOut,
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "agent name (required, must be unique)")
	cmd.Flags().StringVar(&task, "task", "", "short task description (required)")
	cmd.Flags().StringVar(&branch, "branch", "", "branch the agent will work on (required, must exist)")
	cmd.Flags().StringVar(&base, "base", "", "base branch (defaults to config.default_base_branch)")
	cmd.Flags().StringVar(&worktree, "worktree", "", "optional informational worktree path")
	cmd.Flags().StringArrayVar(&claimSpecs, "claim", nil,
		"inline claim in `kind:pattern:intent` format; may be repeated")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired("task")
	_ = cmd.MarkFlagRequired("branch")
	return cmd
}

func newAgentListCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List registered agents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAgentList(cmd, jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return cmd
}

func newAgentShowCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "show <agent>",
		Short: "Show detail for one agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentShow(cmd, args[0], jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return cmd
}

// --- handlers --------------------------------------------------------------

type agentAddOpts struct {
	Name, Task, Branch, Base, Worktree string
	ClaimSpecs                         []string
	JSON                               bool
}

func runAgentAdd(cmd *cobra.Command, o agentAddOpts) error {
	cc, err := openCmdContext()
	if err != nil {
		return err
	}
	defer cc.Close()
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	if o.Base == "" {
		o.Base = cc.Cfg.DefaultBaseBranch
	}

	if ok, err := git.RefExists(cc.Repo, o.Branch); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("branch %q does not exist in %s", o.Branch, cc.Repo)
	}
	if ok, err := git.RefExists(cc.Repo, o.Base); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("base branch %q does not exist in %s", o.Base, cc.Repo)
	}

	baseCommit, err := git.MergeBase(cc.Repo, o.Branch, o.Base)
	if err != nil {
		return err
	}

	agentID := uuid.NewString()
	now := time.Now().UTC()

	newClaims := make([]core.Claim, 0, len(o.ClaimSpecs))
	for _, spec := range o.ClaimSpecs {
		k, p, in, err := ParseClaim(spec)
		if err != nil {
			return err
		}
		c := core.Claim{
			ID:        uuid.NewString(),
			AgentID:   agentID,
			Kind:      k,
			Pattern:   p,
			Intent:    in,
			CreatedAt: now,
		}
		if err := policy.ValidateClaim(c); err != nil {
			return fmt.Errorf("claim %q: %w", spec, err)
		}
		newClaims = append(newClaims, c)
	}

	if len(newClaims) > 0 {
		existing, err := cc.Store.ListClaims(ctx)
		if err != nil {
			return err
		}
		agents, err := cc.Store.ListAgents(ctx)
		if err != nil {
			return err
		}
		nameByID := agentNameMap(agents)
		for _, c := range newClaims {
			v, err := policy.CheckOverlap(c, existing)
			if err != nil {
				return err
			}
			if v.HasConflict() {
				return overlapRefusal(c, v, nameByID)
			}
		}
	}

	agent := core.Agent{
		ID:           agentID,
		Name:         o.Name,
		Task:         o.Task,
		Branch:       o.Branch,
		BaseBranch:   o.Base,
		BaseCommit:   baseCommit,
		WorktreePath: o.Worktree,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := cc.Store.CreateAgentWithClaims(ctx, agent, newClaims); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("agent name %q is already in use", o.Name)
		}
		return err
	}

	if o.JSON {
		return writeJSON(cmd.OutOrStdout(), toAgentView(agent, newClaims))
	}
	renderAgentAdded(cmd.OutOrStdout(), agent, newClaims)
	return nil
}

func runAgentList(cmd *cobra.Command, jsonOut bool) error {
	cc, err := openCmdContext()
	if err != nil {
		return err
	}
	defer cc.Close()
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	agents, err := cc.Store.ListAgents(ctx)
	if err != nil {
		return err
	}
	claims, err := cc.Store.ListClaims(ctx)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, c := range claims {
		counts[c.AgentID]++
	}

	if jsonOut {
		views := make([]agentView, 0, len(agents))
		claimsByAgent := groupClaimsByAgent(claims)
		for _, a := range agents {
			views = append(views, toAgentView(a, claimsByAgent[a.ID]))
		}
		return writeJSON(cmd.OutOrStdout(), views)
	}
	renderAgentList(cmd.OutOrStdout(), agents, counts)
	return nil
}

func runAgentShow(cmd *cobra.Command, name string, jsonOut bool) error {
	cc, err := openCmdContext()
	if err != nil {
		return err
	}
	defer cc.Close()
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	agent, err := cc.Store.GetAgentByName(ctx, name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("agent %q not found", name)
		}
		return err
	}
	claims, err := cc.Store.ListClaimsByAgent(ctx, agent.ID)
	if err != nil {
		return err
	}

	if jsonOut {
		return writeJSON(cmd.OutOrStdout(), toAgentView(agent, claims))
	}
	renderAgentShow(cmd.OutOrStdout(), agent, claims)
	return nil
}

// --- helpers ---------------------------------------------------------------

func agentNameMap(agents []core.Agent) map[string]string {
	m := make(map[string]string, len(agents))
	for _, a := range agents {
		m[a.ID] = a.Name
	}
	return m
}

func groupClaimsByAgent(claims []core.Claim) map[string][]core.Claim {
	m := map[string][]core.Claim{}
	for _, c := range claims {
		m[c.AgentID] = append(m[c.AgentID], c)
	}
	return m
}

func overlapRefusal(newClaim core.Claim, v policy.OverlapVerdict, nameByID map[string]string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "claim %s:%s:%s conflicts with existing claims:",
		newClaim.Kind, newClaim.Pattern, newClaim.Intent)
	for _, c := range v.Conflicts {
		name := nameByID[c.With.AgentID]
		if name == "" {
			name = c.With.AgentID
		}
		fmt.Fprintf(&b, "\n  %s holds: %s %s %s",
			name, c.With.Kind, c.With.Pattern, c.With.Intent)
	}
	return &PolicyError{Msg: b.String()}
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
