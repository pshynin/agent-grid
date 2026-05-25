package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/pshynin/agent-grid/internal/core"
	"github.com/pshynin/agent-grid/internal/policy"
	"github.com/pshynin/agent-grid/internal/store"
)

func newClaimCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "claim",
		Short: "Manage claims",
	}
	cmd.AddCommand(newClaimAddCmd(), newClaimListCmd(), newClaimCheckCmd())
	return cmd
}

func newClaimAddCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "add <agent> <kind:pattern:intent> [<kind:pattern:intent>...]",
		Short: "Add one or more claims to an existing agent",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClaimAdd(cmd, args[0], args[1:], jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return cmd
}

func newClaimListCmd() *cobra.Command {
	var agentName string
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List claims",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runClaimList(cmd, agentName, jsonOut)
		},
	}
	cmd.Flags().StringVar(&agentName, "agent", "", "limit to claims of this agent")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return cmd
}

func newClaimCheckCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "check <kind:pattern:intent>",
		Short: "Report conflicts that would occur if a new agent added this claim (no writes)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClaimCheck(cmd, args[0], jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	return cmd
}

// --- handlers --------------------------------------------------------------

func runClaimAdd(cmd *cobra.Command, agentName string, specs []string, jsonOut bool) error {
	cc, err := openCmdContext()
	if err != nil {
		return err
	}
	defer cc.Close()
	ctx := cmdCtx(cmd)

	agent, err := cc.Store.GetAgentByName(ctx, agentName)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("agent %q not found", agentName)
		}
		return err
	}

	now := time.Now().UTC()
	newClaims := make([]core.Claim, 0, len(specs))
	for _, spec := range specs {
		k, p, in, err := ParseClaim(spec)
		if err != nil {
			return err
		}
		c := core.Claim{
			ID:        uuid.NewString(),
			AgentID:   agent.ID,
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

	for _, c := range newClaims {
		if err := cc.Store.CreateClaim(ctx, c); err != nil {
			return fmt.Errorf("insert claim: %w", err)
		}
	}

	if jsonOut {
		views := make([]claimView, 0, len(newClaims))
		for _, c := range newClaims {
			views = append(views, toClaimView(c, agent.Name))
		}
		return writeJSON(cmd.OutOrStdout(), views)
	}
	out := cmd.OutOrStdout()
	for _, c := range newClaims {
		fmt.Fprintf(out, "claim added: %s %s %s\n", c.Kind, c.Pattern, c.Intent)
	}
	fmt.Fprintf(out, "%d claim(s) added to %s\n", len(newClaims), agent.Name)
	return nil
}

func runClaimList(cmd *cobra.Command, agentName string, jsonOut bool) error {
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
	nameByID := agentNameMap(agents)

	var claims []core.Claim
	if agentName != "" {
		a, err := cc.Store.GetAgentByName(ctx, agentName)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("agent %q not found", agentName)
			}
			return err
		}
		claims, err = cc.Store.ListClaimsByAgent(ctx, a.ID)
		if err != nil {
			return err
		}
	} else {
		claims, err = cc.Store.ListClaims(ctx)
		if err != nil {
			return err
		}
	}

	if jsonOut {
		views := make([]claimView, 0, len(claims))
		for _, c := range claims {
			views = append(views, toClaimView(c, nameByID[c.AgentID]))
		}
		return writeJSON(cmd.OutOrStdout(), views)
	}
	renderClaimList(cmd.OutOrStdout(), claims, nameByID)
	return nil
}

func runClaimCheck(cmd *cobra.Command, spec string, jsonOut bool) error {
	cc, err := openCmdContext()
	if err != nil {
		return err
	}
	defer cc.Close()
	ctx := cmdCtx(cmd)

	k, p, in, err := ParseClaim(spec)
	if err != nil {
		return err
	}
	probe := core.Claim{
		// A nonce AgentID guarantees the probe never matches an existing
		// claim by the same-agent rule.
		ID:        "probe",
		AgentID:   "probe-" + uuid.NewString(),
		Kind:      k,
		Pattern:   p,
		Intent:    in,
		CreatedAt: time.Now().UTC(),
	}
	if err := policy.ValidateClaim(probe); err != nil {
		return fmt.Errorf("claim %q: %w", spec, err)
	}

	existing, err := cc.Store.ListClaims(ctx)
	if err != nil {
		return err
	}
	agents, err := cc.Store.ListAgents(ctx)
	if err != nil {
		return err
	}
	nameByID := agentNameMap(agents)

	v, err := policy.CheckOverlap(probe, existing)
	if err != nil {
		return err
	}

	if jsonOut {
		type conflict struct {
			Agent   string `json:"agent"`
			Kind    string `json:"kind"`
			Pattern string `json:"pattern"`
			Intent  string `json:"intent"`
		}
		type result struct {
			Conflict  bool       `json:"conflict"`
			Conflicts []conflict `json:"conflicts"`
		}
		r := result{Conflict: v.HasConflict()}
		for _, c := range v.Conflicts {
			name := nameByID[c.With.AgentID]
			if name == "" {
				name = c.With.AgentID
			}
			r.Conflicts = append(r.Conflicts, conflict{
				Agent: name, Kind: string(c.With.Kind),
				Pattern: c.With.Pattern, Intent: string(c.With.Intent),
			})
		}
		if err := writeJSON(cmd.OutOrStdout(), r); err != nil {
			return err
		}
		if v.HasConflict() {
			return &PolicyError{Msg: "claim would conflict"}
		}
		return nil
	}

	if !v.HasConflict() {
		fmt.Fprintln(cmd.OutOrStdout(), "no conflicts.")
		return nil
	}
	return overlapRefusal(probe, v, nameByID)
}

// --- shared ---------------------------------------------------------------

func cmdCtx(cmd *cobra.Command) context.Context {
	if ctx := cmd.Context(); ctx != nil {
		return ctx
	}
	return context.Background()
}
