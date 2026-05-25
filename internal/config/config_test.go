package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
}

func TestWriteDefaultRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteDefault(path, "trunk"); err != nil {
		t.Fatalf("WriteDefault: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Version != Version {
		t.Errorf("Version = %d, want %d", cfg.Version, Version)
	}
	if cfg.DefaultBaseBranch != "trunk" {
		t.Errorf("DefaultBaseBranch = %q, want trunk", cfg.DefaultBaseBranch)
	}
	if len(cfg.ForbiddenPaths) == 0 {
		t.Error("ForbiddenPaths unexpectedly empty after round trip")
	}
	if len(cfg.TestFileGlobs) == 0 {
		t.Error("TestFileGlobs unexpectedly empty after round trip")
	}
	want := Default().DiffRisk.Thresholds
	want.FilesLow = cfg.DiffRisk.Thresholds.FilesLow
	want.FilesMedium = cfg.DiffRisk.Thresholds.FilesMedium
	want.FilesHigh = cfg.DiffRisk.Thresholds.FilesHigh
	want.LinesLow = cfg.DiffRisk.Thresholds.LinesLow
	want.LinesMedium = cfg.DiffRisk.Thresholds.LinesMedium
	want.LinesHigh = cfg.DiffRisk.Thresholds.LinesHigh
	if cfg.DiffRisk.Thresholds != want {
		t.Errorf("Thresholds mismatch after round trip: %+v", cfg.DiffRisk.Thresholds)
	}
}

func TestWriteDefaultBlankBranchFallsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := WriteDefault(path, ""); err != nil {
		t.Fatalf("WriteDefault: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultBaseBranch != "main" {
		t.Errorf("DefaultBaseBranch = %q, want main", cfg.DefaultBaseBranch)
	}
}

func TestLoadPartialConfigUsesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := strings.TrimSpace(`
version: 1
default_base_branch: develop
diff_risk:
  thresholds:
    files_high: 50
`)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultBaseBranch != "develop" {
		t.Errorf("DefaultBaseBranch = %q, want develop", cfg.DefaultBaseBranch)
	}
	if cfg.DiffRisk.Thresholds.FilesHigh != 50 {
		t.Errorf("FilesHigh = %d, want 50", cfg.DiffRisk.Thresholds.FilesHigh)
	}
	if cfg.DiffRisk.Thresholds.FilesLow != 5 {
		t.Errorf("FilesLow = %d, want default 5", cfg.DiffRisk.Thresholds.FilesLow)
	}
	if cfg.DiffRisk.Thresholds.LinesLow != 200 {
		t.Errorf("LinesLow = %d, want default 200", cfg.DiffRisk.Thresholds.LinesLow)
	}
	if len(cfg.ForbiddenPaths) == 0 {
		t.Error("ForbiddenPaths should have inherited defaults")
	}
	if len(cfg.TestFileGlobs) == 0 {
		t.Error("TestFileGlobs should have inherited defaults")
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: : :"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestValidateRejectsBadConfig(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(*Config)
		wantSubstr string
	}{
		{
			name:       "wrong version",
			mutate:     func(c *Config) { c.Version = 2 },
			wantSubstr: "version",
		},
		{
			name:       "blank base branch",
			mutate:     func(c *Config) { c.DefaultBaseBranch = "   " },
			wantSubstr: "default_base_branch",
		},
		{
			name: "files out of order",
			mutate: func(c *Config) {
				c.DiffRisk.Thresholds.FilesLow = 100
				c.DiffRisk.Thresholds.FilesMedium = 50
				c.DiffRisk.Thresholds.FilesHigh = 200
			},
			wantSubstr: "files_*",
		},
		{
			name: "lines out of order",
			mutate: func(c *Config) {
				c.DiffRisk.Thresholds.LinesMedium = 100
				c.DiffRisk.Thresholds.LinesHigh = 50
			},
			wantSubstr: "lines_*",
		},
		{
			name:       "negative files threshold",
			mutate:     func(c *Config) { c.DiffRisk.Thresholds.FilesLow = -1 },
			wantSubstr: "files_*",
		},
		{
			name:       "zero lines threshold",
			mutate:     func(c *Config) { c.DiffRisk.Thresholds.LinesLow = 0 },
			wantSubstr: "lines_*",
		},
		{
			name: "blank forbidden path",
			mutate: func(c *Config) {
				c.ForbiddenPaths = append(c.ForbiddenPaths, "   ")
			},
			wantSubstr: "forbidden_paths",
		},
		{
			name: "blank test glob",
			mutate: func(c *Config) {
				c.TestFileGlobs = append(c.TestFileGlobs, "")
			},
			wantSubstr: "test_file_globs",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Default()
			tc.mutate(&cfg)
			err := cfg.Validate()
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

func TestPath(t *testing.T) {
	got := Path("/tmp/repo")
	want := filepath.Join("/tmp/repo", ".agentgrid", "config.yaml")
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}
