package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/pshynin/agent-grid/internal/config"
	"github.com/pshynin/agent-grid/internal/core"
	"github.com/pshynin/agent-grid/internal/git"
	"github.com/pshynin/agent-grid/internal/policy"
	"github.com/pshynin/agent-grid/internal/store"
)

func newDiffRiskCmd() *cobra.Command {
	var (
		jsonOut   bool
		noRefresh bool
	)
	cmd := &cobra.Command{
		Use:   "diff-risk <agent>",
		Short: "Score the agent's branch against thresholds and forbidden paths",
		Long: "Computes a deterministic structural risk verdict from git's diff\n" +
			"statistics, the agent's claims, and the configured forbidden paths.\n" +
			"By default a new snapshot is recomputed and persisted; --no-refresh\n" +
			"reads the most recent persisted snapshot without touching git.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDiffRisk(cmd, args[0], jsonOut, noRefresh)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "JSON output")
	cmd.Flags().BoolVar(&noRefresh, "no-refresh", false,
		"read the latest persisted snapshot instead of recomputing")
	return cmd
}

type diffRiskView struct {
	Agent           string        `json:"agent"`
	Branch          string        `json:"branch"`
	HeadCommit      string        `json:"head_commit"`
	Level           string        `json:"level"`
	Reasons         []core.Reason `json:"reasons"`
	TouchedFiles    []string      `json:"touched_files"`
	ForbiddenHits   []string      `json:"forbidden_hits"`
	ClaimViolations []string      `json:"claim_violations"`
	BinaryFiles     []string      `json:"binary_files,omitempty"`
	FilesChanged    int           `json:"files_changed"`
	LinesAdded      int           `json:"lines_added"`
	LinesRemoved    int           `json:"lines_removed"`
	TakenAt         time.Time     `json:"taken_at"`
}

func runDiffRisk(cmd *cobra.Command, agentName string, jsonOut, noRefresh bool) error {
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

	var view diffRiskView
	if noRefresh {
		view, err = loadDiffSnapshot(ctx, cc, agent)
	} else {
		view, err = recomputeDiffSnapshot(ctx, cc, agent)
	}
	if err != nil {
		return err
	}

	if jsonOut {
		return writeJSON(cmd.OutOrStdout(), view)
	}
	renderDiffRisk(cmd.OutOrStdout(), view)
	return nil
}

func loadDiffSnapshot(ctx context.Context, cc *cmdContext, agent core.Agent) (diffRiskView, error) {
	snap, err := cc.Store.LatestDiffSnapshotByAgent(ctx, agent.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return diffRiskView{}, fmt.Errorf("no diff snapshot for %q; run `agentgrid diff-risk %s` without --no-refresh first",
				agent.Name, agent.Name)
		}
		return diffRiskView{}, err
	}
	return diffSnapshotToView(agent, snap, nil), nil
}

func recomputeDiffSnapshot(ctx context.Context, cc *cmdContext, agent core.Agent) (diffRiskView, error) {
	if ok, err := git.RefExists(cc.Repo, agent.Branch); err != nil {
		return diffRiskView{}, err
	} else if !ok {
		return diffRiskView{}, fmt.Errorf("agent %q: branch %q no longer exists", agent.Name, agent.Branch)
	}
	if ok, err := git.RefExists(cc.Repo, agent.BaseBranch); err != nil {
		return diffRiskView{}, err
	} else if !ok {
		return diffRiskView{}, fmt.Errorf("agent %q: base branch %q no longer exists", agent.Name, agent.BaseBranch)
	}

	// diff range: effectiveBase..branchHead (agent's own work).
	effectiveBase, err := git.MergeBase(cc.Repo, agent.Branch, agent.BaseBranch)
	if err != nil {
		return diffRiskView{}, fmt.Errorf("agent %q: %w", agent.Name, err)
	}
	branchHead, err := git.CurrentHead(cc.Repo, agent.Branch)
	if err != nil {
		return diffRiskView{}, fmt.Errorf("agent %q: %w", agent.Name, err)
	}

	changed, err := git.DiffNameOnly(cc.Repo, effectiveBase, branchHead)
	if err != nil {
		return diffRiskView{}, fmt.Errorf("agent %q: %w", agent.Name, err)
	}
	numstat, err := git.DiffNumstat(cc.Repo, effectiveBase, branchHead)
	if err != nil {
		return diffRiskView{}, fmt.Errorf("agent %q: %w", agent.Name, err)
	}

	files := mergeChangeSources(changed, numstat)

	claims, err := cc.Store.ListClaimsByAgent(ctx, agent.ID)
	if err != nil {
		return diffRiskView{}, fmt.Errorf("agent %q: %w", agent.Name, err)
	}

	verdict := policy.ScoreDiff(policy.DiffRiskInput{
		Files:             files,
		Claims:            claims,
		ForbiddenPatterns: cc.Cfg.ForbiddenPaths,
		Thresholds:        thresholdsFromConfig(cc.Cfg),
	})

	snap := core.DiffSnapshot{
		ID:              uuid.NewString(),
		AgentID:         agent.ID,
		HeadCommit:      branchHead,
		FilesChanged:    verdict.FilesChanged,
		LinesAdded:      verdict.LinesAdded,
		LinesRemoved:    verdict.LinesRemoved,
		TouchedFiles:    verdict.TouchedFiles,
		ForbiddenHits:   verdict.ForbiddenHits,
		ClaimViolations: verdict.ClaimViolations,
		RiskLevel:       verdict.Level,
		RiskReasons:     verdict.Reasons,
		TakenAt:         time.Now().UTC(),
	}
	if err := cc.Store.CreateDiffSnapshot(ctx, snap); err != nil {
		return diffRiskView{}, fmt.Errorf("persist snapshot: %w", err)
	}
	return diffSnapshotToView(agent, snap, verdict.BinaryFiles), nil
}

// mergeChangeSources reconciles DiffNameOnly (paths, with --no-renames so
// renames appear as two paths) with DiffNumstat (stats, rename-aware) into a
// single deduped list of policy.FileChange.
func mergeChangeSources(changed []string, numstat []git.NumstatEntry) []policy.FileChange {
	statsByPath := make(map[string]git.NumstatEntry, len(numstat))
	for _, n := range numstat {
		statsByPath[n.Path] = n
		if n.OldPath != "" {
			statsByPath[n.OldPath] = n
		}
	}
	seen := map[string]bool{}
	out := make([]policy.FileChange, 0, len(changed)+len(numstat))
	for _, p := range changed {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		n := statsByPath[p]
		out = append(out, policy.FileChange{
			Path: p, Added: n.Added, Removed: n.Removed, Binary: n.Binary,
		})
	}
	for _, n := range numstat {
		if seen[n.Path] {
			continue
		}
		seen[n.Path] = true
		out = append(out, policy.FileChange{
			Path: n.Path, Added: n.Added, Removed: n.Removed, Binary: n.Binary,
		})
	}
	return out
}

func thresholdsFromConfig(c config.Config) policy.Thresholds {
	t := c.DiffRisk.Thresholds
	return policy.Thresholds{
		FilesLow: t.FilesLow, FilesMedium: t.FilesMedium, FilesHigh: t.FilesHigh,
		LinesLow: t.LinesLow, LinesMedium: t.LinesMedium, LinesHigh: t.LinesHigh,
	}
}

func diffSnapshotToView(agent core.Agent, snap core.DiffSnapshot, binaries []string) diffRiskView {
	v := diffRiskView{
		Agent:           agent.Name,
		Branch:          agent.Branch,
		HeadCommit:      snap.HeadCommit,
		Level:           string(snap.RiskLevel),
		Reasons:         snap.RiskReasons,
		TouchedFiles:    snap.TouchedFiles,
		ForbiddenHits:   snap.ForbiddenHits,
		ClaimViolations: snap.ClaimViolations,
		BinaryFiles:     binaries,
		FilesChanged:    snap.FilesChanged,
		LinesAdded:      snap.LinesAdded,
		LinesRemoved:    snap.LinesRemoved,
		TakenAt:         snap.TakenAt,
	}
	if v.Reasons == nil {
		v.Reasons = []core.Reason{}
	}
	if v.TouchedFiles == nil {
		v.TouchedFiles = []string{}
	}
	if v.ForbiddenHits == nil {
		v.ForbiddenHits = []string{}
	}
	if v.ClaimViolations == nil {
		v.ClaimViolations = []string{}
	}
	return v
}

func renderDiffRisk(w io.Writer, v diffRiskView) {
	fmt.Fprintf(w, "agent:    %s\n", v.Agent)
	fmt.Fprintf(w, "branch:   %s\n", v.Branch)
	fmt.Fprintf(w, "head:     %s\n", shortSHA(v.HeadCommit))
	fmt.Fprintf(w, "files:    %d\n", v.FilesChanged)
	fmt.Fprintf(w, "lines:    +%d -%d\n", v.LinesAdded, v.LinesRemoved)
	fmt.Fprintf(w, "risk:     %s\n", strings.ToUpper(v.Level))
	if len(v.Reasons) == 0 {
		fmt.Fprintln(w, "reasons:  (none)")
		return
	}
	fmt.Fprintln(w, "reasons:")
	for _, r := range v.Reasons {
		fmt.Fprintf(w, "  %-7s %-26s %s\n", strings.ToUpper(string(r.Severity)), r.Code, r.Detail)
		for _, p := range r.Paths {
			fmt.Fprintf(w, "            %s\n", p)
		}
	}
}
