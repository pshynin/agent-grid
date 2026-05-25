package policy

import (
	"fmt"
	"sort"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/pshynin/agent-grid/internal/core"
)

// Thresholds carries the diff-risk numeric thresholds. low < medium < high
// must hold; the caller (config validation) is responsible for that
// invariant. ScoreDiff does not error on violation; it simply fires the
// highest tier that triggers.
type Thresholds struct {
	FilesLow, FilesMedium, FilesHigh int
	LinesLow, LinesMedium, LinesHigh int
}

// FileChange is the policy-side view of one changed file. The caller maps
// from git.NumstatEntry and DiffNameOnly output before scoring.
type FileChange struct {
	Path    string
	Added   int
	Removed int
	Binary  bool
}

// DiffRiskInput is the immutable input to ScoreDiff.
type DiffRiskInput struct {
	Files             []FileChange
	Claims            []core.Claim
	ForbiddenPatterns []string
	Thresholds        Thresholds
}

// DiffRiskVerdict is the result of ScoreDiff. The slices are stable-sorted
// for deterministic serialization.
type DiffRiskVerdict struct {
	Level           core.RiskLevel
	Reasons         []core.Reason
	TouchedFiles    []string
	ForbiddenHits   []string
	ClaimViolations []string
	BinaryFiles     []string
	FilesChanged    int
	LinesAdded      int
	LinesRemoved    int
}

// ScoreDiff computes a structural diff-risk verdict. Pure: no I/O. Each
// reason fires at most once; the resulting Level is the maximum severity
// across the firing reasons (or RiskLow when no reasons fire).
//
// Reason codes:
//
//   files_over_low / files_over_medium / files_over_high   - mutually exclusive
//   lines_over_low / lines_over_medium / lines_over_high   - mutually exclusive
//   forbidden_path_touched      - any touched file matches a forbidden glob
//   no_claims_with_changes      - agent has zero claims but touched files
//   claim_violation             - 1-2 files outside the agent's claims
//   claim_violation_repeated    - 3+ files outside the agent's claims
//   binary_files_touched        - at least one binary file appears in diff
func ScoreDiff(in DiffRiskInput) DiffRiskVerdict {
	// Dedupe and sort files for stable output.
	byPath := make(map[string]FileChange, len(in.Files))
	for _, f := range in.Files {
		if f.Path == "" {
			continue
		}
		// Last write wins on collisions; we don't expect collisions because
		// the caller dedupes, but be defensive.
		byPath[f.Path] = f
	}
	paths := make([]string, 0, len(byPath))
	for p := range byPath {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var touched, forbidden, violations, binaries []string
	addedTotal, removedTotal := 0, 0

	hasClaims := len(in.Claims) > 0

	for _, p := range paths {
		f := byPath[p]
		touched = append(touched, p)
		addedTotal += f.Added
		removedTotal += f.Removed
		if f.Binary {
			binaries = append(binaries, p)
		}
		if matchesAnyGlob(p, in.ForbiddenPatterns) {
			forbidden = append(forbidden, p)
		}
		if hasClaims && !matchesAnyClaim(p, in.Claims) {
			violations = append(violations, p)
		}
	}

	var reasons []core.Reason

	// File count threshold (mutually exclusive tiers).
	n := len(touched)
	switch {
	case n > in.Thresholds.FilesHigh:
		reasons = append(reasons, core.Reason{
			Code: "files_over_high", Severity: core.SeverityHigh,
			Detail: fmt.Sprintf("%d files (>%d)", n, in.Thresholds.FilesHigh),
		})
	case n > in.Thresholds.FilesMedium:
		reasons = append(reasons, core.Reason{
			Code: "files_over_medium", Severity: core.SeverityMedium,
			Detail: fmt.Sprintf("%d files (>%d)", n, in.Thresholds.FilesMedium),
		})
	case n > in.Thresholds.FilesLow:
		reasons = append(reasons, core.Reason{
			Code: "files_over_low", Severity: core.SeverityLow,
			Detail: fmt.Sprintf("%d files (>%d)", n, in.Thresholds.FilesLow),
		})
	}

	// Lines threshold (mutually exclusive tiers).
	totalLines := addedTotal + removedTotal
	switch {
	case totalLines > in.Thresholds.LinesHigh:
		reasons = append(reasons, core.Reason{
			Code: "lines_over_high", Severity: core.SeverityHigh,
			Detail: fmt.Sprintf("%d lines (>%d)", totalLines, in.Thresholds.LinesHigh),
		})
	case totalLines > in.Thresholds.LinesMedium:
		reasons = append(reasons, core.Reason{
			Code: "lines_over_medium", Severity: core.SeverityMedium,
			Detail: fmt.Sprintf("%d lines (>%d)", totalLines, in.Thresholds.LinesMedium),
		})
	case totalLines > in.Thresholds.LinesLow:
		reasons = append(reasons, core.Reason{
			Code: "lines_over_low", Severity: core.SeverityLow,
			Detail: fmt.Sprintf("%d lines (>%d)", totalLines, in.Thresholds.LinesLow),
		})
	}

	if len(forbidden) > 0 {
		reasons = append(reasons, core.Reason{
			Code: "forbidden_path_touched", Severity: core.SeverityHigh,
			Detail: fmt.Sprintf("%d forbidden path(s)", len(forbidden)),
			Paths:  forbidden,
		})
	}

	switch {
	case !hasClaims && len(touched) > 0:
		reasons = append(reasons, core.Reason{
			Code: "no_claims_with_changes", Severity: core.SeverityMedium,
			Detail: fmt.Sprintf("agent has no claims but %d file(s) changed", len(touched)),
		})
	case len(violations) >= 3:
		reasons = append(reasons, core.Reason{
			Code: "claim_violation_repeated", Severity: core.SeverityHigh,
			Detail: fmt.Sprintf("%d files modified outside claimed scope", len(violations)),
			Paths:  violations,
		})
	case len(violations) >= 1:
		reasons = append(reasons, core.Reason{
			Code: "claim_violation", Severity: core.SeverityMedium,
			Detail: fmt.Sprintf("%d files modified outside claimed scope", len(violations)),
			Paths:  violations,
		})
	}

	if len(binaries) > 0 {
		reasons = append(reasons, core.Reason{
			Code: "binary_files_touched", Severity: core.SeverityMedium,
			Detail: fmt.Sprintf("%d binary file(s)", len(binaries)),
			Paths:  binaries,
		})
	}

	return DiffRiskVerdict{
		Level:           levelFromReasons(reasons),
		Reasons:         reasons,
		TouchedFiles:    touched,
		ForbiddenHits:   forbidden,
		ClaimViolations: violations,
		BinaryFiles:     binaries,
		FilesChanged:    n,
		LinesAdded:      addedTotal,
		LinesRemoved:    removedTotal,
	}
}

func matchesAnyGlob(file string, patterns []string) bool {
	for _, p := range patterns {
		ok, err := doublestar.Match(p, file)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func matchesAnyClaim(file string, claims []core.Claim) bool {
	for _, c := range claims {
		if ClaimMatchesPath(c, file) {
			return true
		}
	}
	return false
}

func levelFromReasons(reasons []core.Reason) core.RiskLevel {
	max := 0
	for _, r := range reasons {
		switch r.Severity {
		case core.SeverityLow:
			if max < 1 {
				max = 1
			}
		case core.SeverityMedium:
			if max < 2 {
				max = 2
			}
		case core.SeverityHigh:
			if max < 3 {
				max = 3
			}
		}
	}
	switch max {
	case 3:
		return core.RiskHigh
	case 2:
		return core.RiskMedium
	default:
		return core.RiskLow
	}
}
