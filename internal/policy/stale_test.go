package policy

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pshynin/agent-grid/internal/core"
)

func claim(kind core.ClaimKind, pattern string, intent core.ClaimIntent) core.Claim {
	return core.Claim{Kind: kind, Pattern: pattern, Intent: intent}
}

func TestClaimMatchesPath(t *testing.T) {
	cases := []struct {
		name  string
		claim core.Claim
		file  string
		want  bool
	}{
		{"path equal", claim(core.ClaimKindPath, "pkg/x.go", core.ClaimIntentEdit), "pkg/x.go", true},
		{"path inequal", claim(core.ClaimKindPath, "pkg/x.go", core.ClaimIntentEdit), "pkg/y.go", false},
		{"path with ./ prefix matches", claim(core.ClaimKindPath, "./pkg/x.go", core.ClaimIntentEdit), "pkg/x.go", true},
		{"path against deeper file no match", claim(core.ClaimKindPath, "pkg/billing", core.ClaimIntentEdit), "pkg/billing/types.go", false},
		{"glob prefix match", claim(core.ClaimKindGlob, "pkg/billing/**", core.ClaimIntentEdit), "pkg/billing/types.go", true},
		{"glob prefix no match", claim(core.ClaimKindGlob, "pkg/billing/**", core.ClaimIntentEdit), "pkg/auth/session.go", false},
		{"glob suffix match", claim(core.ClaimKindGlob, "**/*.go", core.ClaimIntentEdit), "pkg/billing/types.go", true},
		{"glob single segment match", claim(core.ClaimKindGlob, "pkg/*/types.go", core.ClaimIntentEdit), "pkg/billing/types.go", true},
		{"unsupported kind returns false", claim(core.ClaimKind("module"), "billing", core.ClaimIntentEdit), "pkg/billing/types.go", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClaimMatchesPath(tc.claim, tc.file); got != tc.want {
				t.Errorf("ClaimMatchesPath(%+v, %q) = %v, want %v", tc.claim, tc.file, got, tc.want)
			}
		})
	}
}

func TestDeriveStaleMark(t *testing.T) {
	const agentID = "agent-c"

	cases := []struct {
		name       string
		claims     []core.Claim
		changed    []string
		wantStale  bool
		wantRec    core.Recommendation
		wantFiles  []string
		wantReason string
	}{
		{
			name:      "no claims, no stale",
			claims:    nil,
			changed:   []string{"pkg/billing/types.go"},
			wantStale: false,
		},
		{
			name:      "no changed files, no stale",
			claims:    []core.Claim{claim(core.ClaimKindGlob, "pkg/billing/**", core.ClaimIntentRead)},
			changed:   nil,
			wantStale: false,
		},
		{
			name:      "no overlap, no stale",
			claims:    []core.Claim{claim(core.ClaimKindGlob, "pkg/billing/**", core.ClaimIntentEdit)},
			changed:   []string{"pkg/auth/session.go", "README.md"},
			wantStale: false,
		},
		{
			name:       "read claim overlaps once -> review",
			claims:     []core.Claim{claim(core.ClaimKindGlob, "pkg/billing/**", core.ClaimIntentRead)},
			changed:    []string{"pkg/billing/types.go", "pkg/auth/session.go"},
			wantStale:  true,
			wantRec:    core.RecommendReview,
			wantFiles:  []string{"pkg/billing/types.go"},
			wantReason: "base advanced into claimed scope (1 file)",
		},
		{
			name:       "edit claim overlaps once -> rebase",
			claims:     []core.Claim{claim(core.ClaimKindGlob, "pkg/billing/**", core.ClaimIntentEdit)},
			changed:    []string{"pkg/billing/types.go"},
			wantStale:  true,
			wantRec:    core.RecommendRebase,
			wantFiles:  []string{"pkg/billing/types.go"},
			wantReason: "base advanced into claimed scope (1 file)",
		},
		{
			name: "mixed edit + read overlap -> re-plan",
			claims: []core.Claim{
				claim(core.ClaimKindGlob, "pkg/billing/**", core.ClaimIntentEdit),
				claim(core.ClaimKindGlob, "internal/invoice/**", core.ClaimIntentRead),
			},
			changed:    []string{"pkg/billing/types.go", "internal/invoice/api.go"},
			wantStale:  true,
			wantRec:    core.RecommendReplan,
			wantFiles:  []string{"internal/invoice/api.go", "pkg/billing/types.go"},
			wantReason: "base advanced into claimed scope (2 files)",
		},
		{
			name: "file matched by multiple claims counted once",
			claims: []core.Claim{
				claim(core.ClaimKindGlob, "pkg/billing/**", core.ClaimIntentEdit),
				claim(core.ClaimKindGlob, "**/*.go", core.ClaimIntentEdit),
			},
			changed:   []string{"pkg/billing/types.go"},
			wantStale: true,
			wantRec:   core.RecommendRebase,
			wantFiles: []string{"pkg/billing/types.go"},
		},
		{
			name:      "empty file path ignored",
			claims:    []core.Claim{claim(core.ClaimKindGlob, "pkg/**", core.ClaimIntentRead)},
			changed:   []string{"", "pkg/x.go"},
			wantStale: true,
			wantRec:   core.RecommendReview,
			wantFiles: []string{"pkg/x.go"},
		},
		{
			name:      "conflicting files sorted",
			claims:    []core.Claim{claim(core.ClaimKindGlob, "**/*.go", core.ClaimIntentEdit)},
			changed:   []string{"z.go", "a.go", "m.go"},
			wantStale: true,
			wantRec:   core.RecommendRebase,
			wantFiles: []string{"a.go", "m.go", "z.go"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mark, stale := DeriveStaleMark(agentID, tc.claims, tc.changed)
			if stale != tc.wantStale {
				t.Fatalf("stale = %v, want %v (mark=%+v)", stale, tc.wantStale, mark)
			}
			if !stale {
				return
			}
			if mark.AgentID != agentID {
				t.Errorf("AgentID = %q, want %q", mark.AgentID, agentID)
			}
			if mark.Recommendation != tc.wantRec {
				t.Errorf("Recommendation = %q, want %q", mark.Recommendation, tc.wantRec)
			}
			if !reflect.DeepEqual(mark.ConflictingFiles, tc.wantFiles) {
				t.Errorf("ConflictingFiles = %v, want %v", mark.ConflictingFiles, tc.wantFiles)
			}
			if tc.wantReason != "" && mark.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", mark.Reason, tc.wantReason)
			}
			if !strings.Contains(mark.Reason, "claimed scope") {
				t.Errorf("Reason missing 'claimed scope': %q", mark.Reason)
			}
		})
	}
}
