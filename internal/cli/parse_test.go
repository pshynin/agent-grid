package cli

import (
	"strings"
	"testing"

	"github.com/pshynin/agent-grid/internal/core"
)

func TestParseClaim(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantKind    core.ClaimKind
		wantPattern string
		wantIntent  core.ClaimIntent
		wantErr     bool
		errSubstr   string
	}{
		{
			name:        "glob edit",
			in:          "glob:pkg/billing/**:edit",
			wantKind:    "glob",
			wantPattern: "pkg/billing/**",
			wantIntent:  "edit",
		},
		{
			name:        "path read",
			in:          "path:pkg/x.go:read",
			wantKind:    "path",
			wantPattern: "pkg/x.go",
			wantIntent:  "read",
		},
		{
			name:        "pattern containing colon preserved",
			in:          "glob:pkg/a:b/x:edit",
			wantKind:    "glob",
			wantPattern: "pkg/a:b/x",
			wantIntent:  "edit",
		},
		{
			name:        "whitespace trimmed in kind and intent",
			in:          " glob : pkg/billing/** : edit ",
			wantKind:    "glob",
			wantPattern: "pkg/billing/**",
			wantIntent:  "edit",
		},
		{
			name:      "no colons",
			in:        "invalid",
			wantErr:   true,
			errSubstr: "expected",
		},
		{
			name:      "one colon only",
			in:        "glob:pkg/x",
			wantErr:   true,
			errSubstr: "expected",
		},
		{
			name:      "empty kind",
			in:        ":pkg/x:edit",
			wantErr:   true,
			errSubstr: "empty field",
		},
		{
			name:      "empty pattern",
			in:        "glob: :edit",
			wantErr:   true,
			errSubstr: "empty field",
		},
		{
			name:      "empty intent",
			in:        "glob:pkg/x:",
			wantErr:   true,
			errSubstr: "empty field",
		},
		{
			name:      "empty input",
			in:        "",
			wantErr:   true,
			errSubstr: "expected",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, p, i, err := ParseClaim(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got kind=%q pattern=%q intent=%q", k, p, i)
				}
				if !strings.Contains(err.Error(), tc.errSubstr) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if k != tc.wantKind || p != tc.wantPattern || i != tc.wantIntent {
				t.Errorf("got (%q, %q, %q), want (%q, %q, %q)",
					k, p, i, tc.wantKind, tc.wantPattern, tc.wantIntent)
			}
		})
	}
}
