package policy

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/pshynin/agent-grid/internal/core"
)

func defaultThresholds() Thresholds {
	return Thresholds{
		FilesLow: 5, FilesMedium: 15, FilesHigh: 30,
		LinesLow: 200, LinesMedium: 600, LinesHigh: 1500,
	}
}

func files(specs ...FileChange) []FileChange { return specs }

func nFiles(n, addedEach int) []FileChange {
	out := make([]FileChange, n)
	for i := 0; i < n; i++ {
		out[i] = FileChange{Path: fmt.Sprintf("pkg/x/f%02d.go", i), Added: addedEach}
	}
	return out
}

func TestScoreDiffNoChanges(t *testing.T) {
	v := ScoreDiff(DiffRiskInput{Thresholds: defaultThresholds()})
	if v.Level != core.RiskLow {
		t.Errorf("Level = %q, want low", v.Level)
	}
	if len(v.Reasons) != 0 {
		t.Errorf("Reasons should be empty: %+v", v.Reasons)
	}
	if v.FilesChanged != 0 || v.LinesAdded != 0 || v.LinesRemoved != 0 {
		t.Errorf("zero counters expected, got %+v", v)
	}
}

func TestScoreDiffWithinAllThresholds(t *testing.T) {
	claims := []core.Claim{{Kind: core.ClaimKindGlob, Pattern: "pkg/**", Intent: core.ClaimIntentEdit}}
	v := ScoreDiff(DiffRiskInput{
		Files: files(
			FileChange{Path: "pkg/x.go", Added: 50, Removed: 10},
			FileChange{Path: "pkg/y.go", Added: 20, Removed: 5},
		),
		Claims:     claims,
		Thresholds: defaultThresholds(),
	})
	if v.Level != core.RiskLow {
		t.Errorf("Level = %q, want low (reasons=%+v)", v.Level, v.Reasons)
	}
	if len(v.Reasons) != 0 {
		t.Errorf("Reasons should be empty: %+v", v.Reasons)
	}
	if v.FilesChanged != 2 || v.LinesAdded != 70 || v.LinesRemoved != 15 {
		t.Errorf("counters wrong: %+v", v)
	}
}

func TestScoreDiffFileThresholdsMutuallyExclusive(t *testing.T) {
	cases := []struct {
		name     string
		fileN    int
		wantCode string
		wantLvl  core.RiskLevel
	}{
		{"over low only", 6, "files_over_low", core.RiskLow},
		{"over medium", 16, "files_over_medium", core.RiskMedium},
		{"over high", 31, "files_over_high", core.RiskHigh},
		{"at low not over", 5, "", core.RiskLow},
	}
	claims := []core.Claim{{Kind: core.ClaimKindGlob, Pattern: "pkg/**", Intent: core.ClaimIntentEdit}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := ScoreDiff(DiffRiskInput{
				Files:      nFiles(tc.fileN, 1),
				Claims:     claims,
				Thresholds: defaultThresholds(),
			})
			if v.Level != tc.wantLvl {
				t.Errorf("Level = %q, want %q (reasons=%+v)", v.Level, tc.wantLvl, v.Reasons)
			}
			fileReasons := 0
			for _, r := range v.Reasons {
				if r.Code == tc.wantCode {
					fileReasons++
				}
				if tc.wantCode == "" && (r.Code == "files_over_low" ||
					r.Code == "files_over_medium" || r.Code == "files_over_high") {
					t.Errorf("unexpected file reason fired: %+v", r)
				}
			}
			if tc.wantCode != "" && fileReasons != 1 {
				t.Errorf("want exactly one %q reason, got %d (reasons=%+v)", tc.wantCode, fileReasons, v.Reasons)
			}
		})
	}
}

func TestScoreDiffLinesThresholdsMutuallyExclusive(t *testing.T) {
	cases := []struct {
		name    string
		added   int
		wantLvl core.RiskLevel
		wantCode string
	}{
		{"over lines low only", 201, core.RiskLow, "lines_over_low"},
		{"over lines medium", 700, core.RiskMedium, "lines_over_medium"},
		{"over lines high", 2000, core.RiskHigh, "lines_over_high"},
	}
	claims := []core.Claim{{Kind: core.ClaimKindGlob, Pattern: "pkg/**", Intent: core.ClaimIntentEdit}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := ScoreDiff(DiffRiskInput{
				Files:      files(FileChange{Path: "pkg/x.go", Added: tc.added}),
				Claims:     claims,
				Thresholds: defaultThresholds(),
			})
			if v.Level != tc.wantLvl {
				t.Errorf("Level = %q, want %q", v.Level, tc.wantLvl)
			}
			hit := false
			for _, r := range v.Reasons {
				if r.Code == tc.wantCode {
					hit = true
				}
			}
			if !hit {
				t.Errorf("missing %q reason: %+v", tc.wantCode, v.Reasons)
			}
		})
	}
}

func TestScoreDiffForbiddenPathTouched(t *testing.T) {
	claims := []core.Claim{{Kind: core.ClaimKindGlob, Pattern: "pkg/**", Intent: core.ClaimIntentEdit}}
	v := ScoreDiff(DiffRiskInput{
		Files: files(
			FileChange{Path: "pkg/x.go", Added: 1},
			FileChange{Path: "vendor/lib.go", Added: 1},
		),
		Claims:            claims,
		ForbiddenPatterns: []string{"vendor/**"},
		Thresholds:        defaultThresholds(),
	})
	if v.Level != core.RiskHigh {
		t.Errorf("Level = %q, want high", v.Level)
	}
	if len(v.ForbiddenHits) != 1 || v.ForbiddenHits[0] != "vendor/lib.go" {
		t.Errorf("ForbiddenHits wrong: %+v", v.ForbiddenHits)
	}
	if !hasReason(v.Reasons, "forbidden_path_touched") {
		t.Errorf("missing forbidden_path_touched: %+v", v.Reasons)
	}
}

func TestScoreDiffClaimViolationOneToTwoFiles(t *testing.T) {
	claims := []core.Claim{{Kind: core.ClaimKindGlob, Pattern: "pkg/billing/**", Intent: core.ClaimIntentEdit}}
	v := ScoreDiff(DiffRiskInput{
		Files: files(
			FileChange{Path: "pkg/billing/types.go", Added: 5},
			FileChange{Path: "pkg/auth/session.go", Added: 5},
		),
		Claims:     claims,
		Thresholds: defaultThresholds(),
	})
	if v.Level != core.RiskMedium {
		t.Errorf("Level = %q, want medium", v.Level)
	}
	if !hasReason(v.Reasons, "claim_violation") {
		t.Errorf("missing claim_violation: %+v", v.Reasons)
	}
	if hasReason(v.Reasons, "claim_violation_repeated") {
		t.Errorf("should not fire repeated yet")
	}
	want := []string{"pkg/auth/session.go"}
	if !reflect.DeepEqual(v.ClaimViolations, want) {
		t.Errorf("ClaimViolations = %v, want %v", v.ClaimViolations, want)
	}
}

func TestScoreDiffClaimViolationRepeated(t *testing.T) {
	claims := []core.Claim{{Kind: core.ClaimKindGlob, Pattern: "pkg/billing/**", Intent: core.ClaimIntentEdit}}
	v := ScoreDiff(DiffRiskInput{
		Files: files(
			FileChange{Path: "pkg/billing/types.go", Added: 1},
			FileChange{Path: "pkg/auth/a.go", Added: 1},
			FileChange{Path: "pkg/auth/b.go", Added: 1},
			FileChange{Path: "pkg/auth/c.go", Added: 1},
		),
		Claims:     claims,
		Thresholds: defaultThresholds(),
	})
	if v.Level != core.RiskHigh {
		t.Errorf("Level = %q, want high", v.Level)
	}
	if !hasReason(v.Reasons, "claim_violation_repeated") {
		t.Errorf("missing claim_violation_repeated: %+v", v.Reasons)
	}
	if hasReason(v.Reasons, "claim_violation") {
		t.Errorf("should not also fire claim_violation")
	}
}

func TestScoreDiffNoClaimsWithChanges(t *testing.T) {
	v := ScoreDiff(DiffRiskInput{
		Files:      files(FileChange{Path: "pkg/x.go", Added: 1}),
		Thresholds: defaultThresholds(),
	})
	if v.Level != core.RiskMedium {
		t.Errorf("Level = %q, want medium", v.Level)
	}
	if !hasReason(v.Reasons, "no_claims_with_changes") {
		t.Errorf("missing no_claims_with_changes: %+v", v.Reasons)
	}
	if hasReason(v.Reasons, "claim_violation") || hasReason(v.Reasons, "claim_violation_repeated") {
		t.Errorf("violation reasons should not fire when there are no claims at all")
	}
}

func TestScoreDiffBinaryFiles(t *testing.T) {
	claims := []core.Claim{{Kind: core.ClaimKindGlob, Pattern: "**", Intent: core.ClaimIntentEdit}}
	v := ScoreDiff(DiffRiskInput{
		Files: files(
			FileChange{Path: "image.bin", Binary: true},
			FileChange{Path: "pkg/x.go", Added: 1},
		),
		Claims:     claims,
		Thresholds: defaultThresholds(),
	})
	if v.Level != core.RiskMedium {
		t.Errorf("Level = %q, want medium", v.Level)
	}
	if !hasReason(v.Reasons, "binary_files_touched") {
		t.Errorf("missing binary_files_touched: %+v", v.Reasons)
	}
	if len(v.BinaryFiles) != 1 || v.BinaryFiles[0] != "image.bin" {
		t.Errorf("BinaryFiles = %+v", v.BinaryFiles)
	}
}

func TestScoreDiffStackedReasonsLevelIsMax(t *testing.T) {
	claims := []core.Claim{{Kind: core.ClaimKindGlob, Pattern: "pkg/**", Intent: core.ClaimIntentEdit}}
	in := DiffRiskInput{
		Files: append(nFiles(31, 80),
			FileChange{Path: "vendor/lib.go", Added: 1},
			FileChange{Path: "image.bin", Binary: true},
			FileChange{Path: "scripts/deploy.sh", Added: 5},
		),
		Claims:            claims,
		ForbiddenPatterns: []string{"vendor/**"},
		Thresholds:        defaultThresholds(),
	}
	v := ScoreDiff(in)
	if v.Level != core.RiskHigh {
		t.Errorf("Level = %q, want high (reasons=%+v)", v.Level, v.Reasons)
	}
	mustFire := []string{
		"files_over_high",
		"lines_over_high",
		"forbidden_path_touched",
		"claim_violation_repeated",
		"binary_files_touched",
	}
	for _, code := range mustFire {
		if !hasReason(v.Reasons, code) {
			t.Errorf("missing reason %q (reasons=%+v)", code, v.Reasons)
		}
	}
}

func TestScoreDiffTouchedFilesAreSortedAndDeduped(t *testing.T) {
	claims := []core.Claim{{Kind: core.ClaimKindGlob, Pattern: "**", Intent: core.ClaimIntentEdit}}
	v := ScoreDiff(DiffRiskInput{
		Files: files(
			FileChange{Path: "z.go", Added: 1},
			FileChange{Path: "a.go", Added: 1},
			FileChange{Path: "a.go", Added: 2}, // dup
			FileChange{Path: "m.go", Added: 1},
		),
		Claims:     claims,
		Thresholds: defaultThresholds(),
	})
	want := []string{"a.go", "m.go", "z.go"}
	if !reflect.DeepEqual(v.TouchedFiles, want) {
		t.Errorf("TouchedFiles = %v, want %v", v.TouchedFiles, want)
	}
	if v.FilesChanged != 3 {
		t.Errorf("FilesChanged = %d, want 3", v.FilesChanged)
	}
}

func TestScoreDiffEmptyPathIgnored(t *testing.T) {
	claims := []core.Claim{{Kind: core.ClaimKindGlob, Pattern: "**", Intent: core.ClaimIntentEdit}}
	v := ScoreDiff(DiffRiskInput{
		Files: files(
			FileChange{Path: "", Added: 999},
			FileChange{Path: "a.go", Added: 1},
		),
		Claims:     claims,
		Thresholds: defaultThresholds(),
	})
	if v.FilesChanged != 1 || v.LinesAdded != 1 {
		t.Errorf("counters wrong: %+v", v)
	}
}

func hasReason(reasons []core.Reason, code string) bool {
	for _, r := range reasons {
		if r.Code == code {
			return true
		}
	}
	return false
}
