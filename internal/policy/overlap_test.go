package policy

import (
	"strings"
	"testing"

	"github.com/pshynin/agent-grid/internal/core"
)

func mkClaim(id, agent string, kind core.ClaimKind, pattern string, intent core.ClaimIntent) core.Claim {
	return core.Claim{
		ID:      id,
		AgentID: agent,
		Kind:    kind,
		Pattern: pattern,
		Intent:  intent,
	}
}

func TestCheckOverlap(t *testing.T) {
	const (
		edit = core.ClaimIntentEdit
		read = core.ClaimIntentRead
		pth  = core.ClaimKindPath
		gl   = core.ClaimKindGlob
	)

	cases := []struct {
		name     string
		newC     core.Claim
		existing []core.Claim
		want     bool
	}{
		// ---- intent rules ----------------------------------------------
		{
			name: "read+read same path: no conflict",
			newC: mkClaim("n", "A", pth, "pkg/x.go", read),
			existing: []core.Claim{
				mkClaim("e", "B", pth, "pkg/x.go", read),
			},
			want: false,
		},
		{
			name: "edit+read same path: conflict",
			newC: mkClaim("n", "A", pth, "pkg/x.go", edit),
			existing: []core.Claim{
				mkClaim("e", "B", pth, "pkg/x.go", read),
			},
			want: true,
		},
		{
			name: "read+edit same path: conflict",
			newC: mkClaim("n", "A", pth, "pkg/x.go", read),
			existing: []core.Claim{
				mkClaim("e", "B", pth, "pkg/x.go", edit),
			},
			want: true,
		},
		{
			name: "edit+edit same path: conflict",
			newC: mkClaim("n", "A", pth, "pkg/x.go", edit),
			existing: []core.Claim{
				mkClaim("e", "B", pth, "pkg/x.go", edit),
			},
			want: true,
		},
		{
			name: "read+read overlapping globs: no conflict",
			newC: mkClaim("n", "A", gl, "pkg/billing/**", read),
			existing: []core.Claim{
				mkClaim("e", "B", gl, "pkg/billing/**", read),
			},
			want: false,
		},

		// ---- exact-path rules ------------------------------------------
		{
			name: "different paths: no conflict",
			newC: mkClaim("n", "A", pth, "pkg/x.go", edit),
			existing: []core.Claim{
				mkClaim("e", "B", pth, "pkg/y.go", edit),
			},
			want: false,
		},
		{
			name: "path equality after cleaning leading ./",
			newC: mkClaim("n", "A", pth, "./pkg/x.go", edit),
			existing: []core.Claim{
				mkClaim("e", "B", pth, "pkg/x.go", edit),
			},
			want: true,
		},
		{
			name: "path equality after trimming trailing slash on directories",
			newC: mkClaim("n", "A", pth, "pkg/billing/", edit),
			existing: []core.Claim{
				mkClaim("e", "B", pth, "pkg/billing", edit),
			},
			want: true,
		},

		// ---- path vs glob (both directions) ----------------------------
		{
			name: "path inside glob: conflict",
			newC: mkClaim("n", "A", pth, "pkg/billing/types.go", edit),
			existing: []core.Claim{
				mkClaim("e", "B", gl, "pkg/billing/**", edit),
			},
			want: true,
		},
		{
			name: "glob covers path (symmetric): conflict",
			newC: mkClaim("n", "A", gl, "pkg/billing/**", edit),
			existing: []core.Claim{
				mkClaim("e", "B", pth, "pkg/billing/types.go", edit),
			},
			want: true,
		},
		{
			name: "path outside glob: no conflict",
			newC: mkClaim("n", "A", pth, "pkg/auth/session.go", edit),
			existing: []core.Claim{
				mkClaim("e", "B", gl, "pkg/billing/**", edit),
			},
			want: false,
		},
		{
			name: "literal glob equals path: conflict",
			newC: mkClaim("n", "A", gl, "pkg/billing/types.go", edit),
			existing: []core.Claim{
				mkClaim("e", "B", pth, "pkg/billing/types.go", edit),
			},
			want: true,
		},
		{
			name: "wildcard segment matches path",
			newC: mkClaim("n", "A", pth, "pkg/billing/types.go", edit),
			existing: []core.Claim{
				mkClaim("e", "B", gl, "pkg/*/types.go", edit),
			},
			want: true,
		},

		// ---- glob vs glob ----------------------------------------------
		{
			name: "identical globs: conflict",
			newC: mkClaim("n", "A", gl, "pkg/billing/**", edit),
			existing: []core.Claim{
				mkClaim("e", "B", gl, "pkg/billing/**", edit),
			},
			want: true,
		},
		{
			name: "nested globs (parent vs child): conflict",
			newC: mkClaim("n", "A", gl, "pkg/billing/**", edit),
			existing: []core.Claim{
				mkClaim("e", "B", gl, "pkg/billing/sub/**", edit),
			},
			want: true,
		},
		{
			name: "sibling globs: no conflict",
			newC: mkClaim("n", "A", gl, "pkg/billing/**", edit),
			existing: []core.Claim{
				mkClaim("e", "B", gl, "pkg/auth/**", edit),
			},
			want: false,
		},
		{
			name: "prefix-glob vs suffix-glob with shared scope: conflict",
			newC: mkClaim("n", "A", gl, "pkg/billing/**", edit),
			existing: []core.Claim{
				mkClaim("e", "B", gl, "**/*.go", edit),
			},
			want: true,
		},
		{
			name: "extension-only globs that share nothing: no conflict",
			newC: mkClaim("n", "A", gl, "**/*.go", edit),
			existing: []core.Claim{
				mkClaim("e", "B", gl, "**/*.ts", edit),
			},
			want: false,
		},
		{
			name: "single-star segment vs different segment: no conflict",
			newC: mkClaim("n", "A", gl, "pkg/*/types.go", edit),
			existing: []core.Claim{
				mkClaim("e", "B", gl, "internal/*/types.go", edit),
			},
			want: false,
		},

		// ---- same agent skip ------------------------------------------
		{
			name: "same agent: existing claim ignored",
			newC: mkClaim("n", "A", gl, "pkg/billing/**", edit),
			existing: []core.Claim{
				mkClaim("e", "A", gl, "pkg/billing/**", edit),
			},
			want: false,
		},

		// ---- aggregation ----------------------------------------------
		{
			name: "one of several existing conflicts",
			newC: mkClaim("n", "A", gl, "pkg/billing/**", edit),
			existing: []core.Claim{
				mkClaim("e1", "B", gl, "pkg/auth/**", edit),
				mkClaim("e2", "C", gl, "pkg/billing/sub/**", edit),
				mkClaim("e3", "D", pth, "internal/x.go", edit),
			},
			want: true,
		},
		{
			name: "no existing claims",
			newC: mkClaim("n", "A", gl, "pkg/billing/**", edit),
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := CheckOverlap(tc.newC, tc.existing)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := v.HasConflict()
			if got != tc.want {
				t.Errorf("HasConflict() = %v, want %v\n  new:      %+v\n  existing: %+v\n  verdict:  %+v",
					got, tc.want, tc.newC, tc.existing, v)
			}
		})
	}
}

func TestCheckOverlapReportsConflictingAgent(t *testing.T) {
	newC := mkClaim("n", "A", core.ClaimKindGlob, "pkg/billing/**", core.ClaimIntentEdit)
	existing := []core.Claim{
		mkClaim("e1", "B", core.ClaimKindGlob, "pkg/auth/**", core.ClaimIntentEdit),
		mkClaim("e2", "C", core.ClaimKindGlob, "pkg/billing/sub/**", core.ClaimIntentEdit),
	}
	v, err := CheckOverlap(newC, existing)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(v.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1: %+v", len(v.Conflicts), v.Conflicts)
	}
	if v.Conflicts[0].With.AgentID != "C" {
		t.Errorf("conflict.With.AgentID = %q, want C", v.Conflicts[0].With.AgentID)
	}
	if v.Conflicts[0].With.Pattern != "pkg/billing/sub/**" {
		t.Errorf("conflict.With.Pattern = %q, want pkg/billing/sub/**", v.Conflicts[0].With.Pattern)
	}
	if v.Conflicts[0].NewPattern != "pkg/billing/**" {
		t.Errorf("conflict.NewPattern = %q, want pkg/billing/**", v.Conflicts[0].NewPattern)
	}
}

func TestValidateClaim(t *testing.T) {
	cases := []struct {
		name       string
		c          core.Claim
		wantOK     bool
		wantSubstr string
	}{
		{
			name:   "valid path edit",
			c:      mkClaim("", "A", core.ClaimKindPath, "pkg/x.go", core.ClaimIntentEdit),
			wantOK: true,
		},
		{
			name:   "valid glob read",
			c:      mkClaim("", "A", core.ClaimKindGlob, "pkg/**", core.ClaimIntentRead),
			wantOK: true,
		},
		{
			name:       "unsupported kind module",
			c:          mkClaim("", "A", core.ClaimKind("module"), "billing", core.ClaimIntentEdit),
			wantSubstr: "kind",
		},
		{
			name:       "empty kind",
			c:          mkClaim("", "A", "", "pkg/x.go", core.ClaimIntentEdit),
			wantSubstr: "kind",
		},
		{
			name:       "unsupported intent create",
			c:          mkClaim("", "A", core.ClaimKindPath, "pkg/x.go", core.ClaimIntent("create")),
			wantSubstr: "intent",
		},
		{
			name:       "empty intent",
			c:          mkClaim("", "A", core.ClaimKindPath, "pkg/x.go", ""),
			wantSubstr: "intent",
		},
		{
			name:       "empty pattern",
			c:          mkClaim("", "A", core.ClaimKindGlob, "   ", core.ClaimIntentEdit),
			wantSubstr: "pattern",
		},
		{
			name:       "malformed glob",
			c:          mkClaim("", "A", core.ClaimKindGlob, "pkg/[unterm", core.ClaimIntentEdit),
			wantSubstr: "glob",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateClaim(tc.c)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("expected ok, got error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantSubstr)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

func TestCheckOverlapRejectsInvalidNewClaim(t *testing.T) {
	bad := mkClaim("n", "A", core.ClaimKind("module"), "billing", core.ClaimIntentEdit)
	_, err := CheckOverlap(bad, nil)
	if err == nil {
		t.Fatal("expected error for invalid new claim")
	}
	if !strings.Contains(err.Error(), "new claim") {
		t.Errorf("error %q should mention 'new claim'", err.Error())
	}
}

func TestSimpleWitness(t *testing.T) {
	cases := []struct {
		pattern string
		want    string
	}{
		{"pkg/billing/types.go", "pkg/billing/types.go"},
		{"pkg/billing/**", "pkg/billing/x"},
		{"pkg/*/types.go", "pkg/x/types.go"},
		{"**/*.go", "x/x.go"},
		{"pkg/?ar.go", "pkg/xar.go"},
		{"pkg/[abc]ar.go", "pkg/aar.go"},
		{"pkg/[!abc]ar.go", "pkg/aar.go"},
		{"pkg/{auth,billing}/x.go", "pkg/auth/x.go"},
		{"pkg/[unterm", "pkg/[unterm"},
		{"pkg/{unterm", "pkg/{unterm"},
		{"pkg/[]ar.go", "pkg/xar.go"},
	}
	for _, tc := range cases {
		t.Run(tc.pattern, func(t *testing.T) {
			got := simpleWitness(tc.pattern)
			if got != tc.want {
				t.Errorf("simpleWitness(%q) = %q, want %q", tc.pattern, got, tc.want)
			}
		})
	}
}

func TestLiteralPrefixSuffix(t *testing.T) {
	cases := []struct {
		pattern string
		prefix  string
		suffix  string
	}{
		{"pkg/billing/**", "pkg/billing/", ""},
		{"**/*.go", "", ".go"},
		{"pkg/*/types.go", "pkg/", "/types.go"},
		{"pkg/billing/types.go", "pkg/billing/types.go", "pkg/billing/types.go"},
		{"*.go", "", ".go"},
	}
	for _, tc := range cases {
		t.Run(tc.pattern, func(t *testing.T) {
			if got := literalPrefix(tc.pattern); got != tc.prefix {
				t.Errorf("literalPrefix(%q) = %q, want %q", tc.pattern, got, tc.prefix)
			}
			if got := literalSuffix(tc.pattern); got != tc.suffix {
				t.Errorf("literalSuffix(%q) = %q, want %q", tc.pattern, got, tc.suffix)
			}
		})
	}
}

func TestNormalizeGlobAllSlashes(t *testing.T) {
	c := mkClaim("", "A", core.ClaimKindGlob, "///", core.ClaimIntentEdit)
	p, isGlob := normalize(c)
	if !isGlob {
		t.Error("expected glob")
	}
	if p != "/" {
		t.Errorf("normalize(///) = %q, want /", p)
	}
}

func TestCheckOverlapRejectsInvalidExistingClaim(t *testing.T) {
	good := mkClaim("n", "A", core.ClaimKindPath, "pkg/x.go", core.ClaimIntentEdit)
	bad := mkClaim("e", "B", core.ClaimKind("module"), "billing", core.ClaimIntentEdit)
	_, err := CheckOverlap(good, []core.Claim{bad})
	if err == nil {
		t.Fatal("expected error for invalid existing claim")
	}
	if !strings.Contains(err.Error(), `"e"`) {
		t.Errorf("error %q should mention existing claim id 'e'", err.Error())
	}
}
