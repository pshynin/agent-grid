package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/pshynin/agent-grid/internal/core"
)

// agentView is the JSON shape for `agent show` / `agent list` output.
type agentView struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Task         string       `json:"task"`
	Branch       string       `json:"branch"`
	BaseBranch   string       `json:"base_branch"`
	BaseCommit   string       `json:"base_commit"`
	WorktreePath string       `json:"worktree_path,omitempty"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
	Claims       []claimView  `json:"claims,omitempty"`
}

type claimView struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	AgentName string    `json:"agent_name,omitempty"`
	Kind      string    `json:"kind"`
	Pattern   string    `json:"pattern"`
	Intent    string    `json:"intent"`
	CreatedAt time.Time `json:"created_at"`
}

func toAgentView(a core.Agent, claims []core.Claim) agentView {
	v := agentView{
		ID:           a.ID,
		Name:         a.Name,
		Task:         a.Task,
		Branch:       a.Branch,
		BaseBranch:   a.BaseBranch,
		BaseCommit:   a.BaseCommit,
		WorktreePath: a.WorktreePath,
		CreatedAt:    a.CreatedAt,
		UpdatedAt:    a.UpdatedAt,
	}
	for _, c := range claims {
		v.Claims = append(v.Claims, toClaimView(c, a.Name))
	}
	return v
}

func toClaimView(c core.Claim, agentName string) claimView {
	return claimView{
		ID:        c.ID,
		AgentID:   c.AgentID,
		AgentName: agentName,
		Kind:      string(c.Kind),
		Pattern:   c.Pattern,
		Intent:    string(c.Intent),
		CreatedAt: c.CreatedAt,
	}
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func renderAgentAdded(w io.Writer, a core.Agent, claims []core.Claim) {
	fmt.Fprintf(w, "agent added: %s (%s)\n", a.Name, a.ID)
	fmt.Fprintf(w, "  branch:   %s\n", a.Branch)
	fmt.Fprintf(w, "  base:     %s @ %s\n", a.BaseBranch, shortSHA(a.BaseCommit))
	if a.WorktreePath != "" {
		fmt.Fprintf(w, "  worktree: %s\n", a.WorktreePath)
	}
	if len(claims) > 0 {
		fmt.Fprintf(w, "  claims:\n")
		renderClaimLines(w, claims, "    ")
	}
}

func renderAgentList(w io.Writer, agents []core.Agent, claimCounts map[string]int) {
	if len(agents) == 0 {
		fmt.Fprintln(w, "no agents registered.")
		return
	}
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "NAME\tBRANCH\tBASE\tCLAIMS\tAGE")
	for _, a := range agents {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n",
			a.Name, a.Branch, a.BaseBranch, claimCounts[a.ID], ageOf(a.CreatedAt))
	}
	_ = tw.Flush()
}

func renderAgentShow(w io.Writer, a core.Agent, claims []core.Claim) {
	fmt.Fprintf(w, "agent: %s (%s)\n", a.Name, a.ID)
	fmt.Fprintf(w, "  task:     %s\n", a.Task)
	fmt.Fprintf(w, "  branch:   %s\n", a.Branch)
	fmt.Fprintf(w, "  base:     %s @ %s\n", a.BaseBranch, shortSHA(a.BaseCommit))
	if a.WorktreePath != "" {
		fmt.Fprintf(w, "  worktree: %s\n", a.WorktreePath)
	}
	fmt.Fprintf(w, "  created:  %s\n", a.CreatedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "  updated:  %s\n", a.UpdatedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(w, "  claims (%d):\n", len(claims))
	if len(claims) > 0 {
		renderClaimLines(w, claims, "    ")
	}
}

func renderClaimLines(w io.Writer, claims []core.Claim, indent string) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range claims {
		fmt.Fprintf(tw, "%s%s\t%s\t%s\n", indent, c.Kind, c.Pattern, c.Intent)
	}
	_ = tw.Flush()
}

func renderClaimList(w io.Writer, claims []core.Claim, agentNameByID map[string]string) {
	if len(claims) == 0 {
		fmt.Fprintln(w, "no claims registered.")
		return
	}
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "AGENT\tKIND\tPATTERN\tINTENT")
	for _, c := range claims {
		name := agentNameByID[c.AgentID]
		if name == "" {
			name = c.AgentID
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", name, c.Kind, c.Pattern, c.Intent)
	}
	_ = tw.Flush()
}

func newTabWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
}

func shortSHA(sha string) string {
	if len(sha) <= 7 {
		return sha
	}
	return sha[:7]
}

func ageOf(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours())/24)
	}
}
