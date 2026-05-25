package policy

import (
	"fmt"
	"path"
	"sort"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/pshynin/agent-grid/internal/core"
)

// ClaimMatchesPath reports whether file falls under the claim's pattern.
// path-kind claims are compared by literal equality (after path.Clean);
// glob-kind claims are matched with doublestar. Invalid claim kinds return
// false rather than panicking.
func ClaimMatchesPath(c core.Claim, file string) bool {
	cleaned := path.Clean(file)
	switch c.Kind {
	case core.ClaimKindPath:
		return path.Clean(c.Pattern) == cleaned
	case core.ClaimKindGlob:
		ok, err := doublestar.Match(c.Pattern, cleaned)
		return err == nil && ok
	}
	return false
}

// DeriveStaleMark inspects files changed on the base branch (since the
// agent's effective base commit) against the agent's claims. It returns a
// stale mark when at least one changed file falls inside one of the agent's
// claims, and (zero, false) otherwise. The function is pure: no I/O.
//
// Recommendation rules:
//   - only edit claim overlaps -> rebase
//   - only read claim overlaps -> review
//   - both intents overlap     -> re-plan
//
// AgentID and the per-file conflict set are populated on the returned mark;
// the caller is responsible for assigning ID and CreatedAt before persisting.
func DeriveStaleMark(agentID string, claims []core.Claim, baseChanged []string) (core.StaleMark, bool) {
	seen := make(map[string]bool, len(baseChanged))
	var conflicts []string
	var hasEdit, hasRead bool

	for _, f := range baseChanged {
		if f == "" {
			continue
		}
		for _, c := range claims {
			if !ClaimMatchesPath(c, f) {
				continue
			}
			if !seen[f] {
				conflicts = append(conflicts, f)
				seen[f] = true
			}
			switch c.Intent {
			case core.ClaimIntentEdit:
				hasEdit = true
			case core.ClaimIntentRead:
				hasRead = true
			}
		}
	}

	if len(conflicts) == 0 {
		return core.StaleMark{}, false
	}
	sort.Strings(conflicts)

	var rec core.Recommendation
	switch {
	case hasEdit && hasRead:
		rec = core.RecommendReplan
	case hasEdit:
		rec = core.RecommendRebase
	case hasRead:
		rec = core.RecommendReview
	default:
		// Reachable only if a claim has an unknown intent; treat as re-plan
		// rather than silently dropping the conflict.
		rec = core.RecommendReplan
	}

	noun := "file"
	if len(conflicts) != 1 {
		noun = "files"
	}
	return core.StaleMark{
		AgentID:          agentID,
		Reason:           fmt.Sprintf("base advanced into claimed scope (%d %s)", len(conflicts), noun),
		ConflictingFiles: conflicts,
		Recommendation:   rec,
	}, true
}
