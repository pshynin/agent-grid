package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const Version = 1

const FileName = "config.yaml"

type Config struct {
	Version           int      `yaml:"version"`
	DefaultBaseBranch string   `yaml:"default_base_branch"`
	ForbiddenPaths    []string `yaml:"forbidden_paths"`
	TestFileGlobs     []string `yaml:"test_file_globs"`
	DiffRisk          DiffRisk `yaml:"diff_risk"`
}

type DiffRisk struct {
	Thresholds Thresholds `yaml:"thresholds"`
}

type Thresholds struct {
	FilesLow    int `yaml:"files_low"`
	FilesMedium int `yaml:"files_medium"`
	FilesHigh   int `yaml:"files_high"`
	LinesLow    int `yaml:"lines_low"`
	LinesMedium int `yaml:"lines_medium"`
	LinesHigh   int `yaml:"lines_high"`
}

func Default() Config {
	return Config{
		Version:           Version,
		DefaultBaseBranch: "main",
		ForbiddenPaths: []string{
			"vendor/**",
			"node_modules/**",
			"migrations/**",
			"infra/prod/**",
		},
		TestFileGlobs: []string{
			"**/*_test.go",
			"**/test/**",
			"**/__tests__/**",
		},
		DiffRisk: DiffRisk{
			Thresholds: Thresholds{
				FilesLow:    5,
				FilesMedium: 15,
				FilesHigh:   30,
				LinesLow:    200,
				LinesMedium: 600,
				LinesHigh:   1500,
			},
		},
	}
}

func Path(repoRoot string) string {
	return filepath.Join(repoRoot, ".agentgrid", FileName)
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	cfg := Default()
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(false)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func WriteDefault(path, baseBranch string) error {
	if baseBranch == "" {
		baseBranch = "main"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	body := fmt.Sprintf(defaultTemplate, baseBranch)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func (c Config) Validate() error {
	var errs []string

	if c.Version != Version {
		errs = append(errs, fmt.Sprintf(
			"version: unsupported (got %d, want %d)", c.Version, Version,
		))
	}
	if strings.TrimSpace(c.DefaultBaseBranch) == "" {
		errs = append(errs, "default_base_branch: must be non-empty")
	}

	t := c.DiffRisk.Thresholds
	if t.FilesLow <= 0 || t.FilesMedium <= 0 || t.FilesHigh <= 0 {
		errs = append(errs, fmt.Sprintf(
			"diff_risk.thresholds.files_*: must be positive (got %d, %d, %d)",
			t.FilesLow, t.FilesMedium, t.FilesHigh,
		))
	} else if !(t.FilesLow < t.FilesMedium && t.FilesMedium < t.FilesHigh) {
		errs = append(errs, fmt.Sprintf(
			"diff_risk.thresholds.files_*: must satisfy low<medium<high (got %d, %d, %d)",
			t.FilesLow, t.FilesMedium, t.FilesHigh,
		))
	}
	if t.LinesLow <= 0 || t.LinesMedium <= 0 || t.LinesHigh <= 0 {
		errs = append(errs, fmt.Sprintf(
			"diff_risk.thresholds.lines_*: must be positive (got %d, %d, %d)",
			t.LinesLow, t.LinesMedium, t.LinesHigh,
		))
	} else if !(t.LinesLow < t.LinesMedium && t.LinesMedium < t.LinesHigh) {
		errs = append(errs, fmt.Sprintf(
			"diff_risk.thresholds.lines_*: must satisfy low<medium<high (got %d, %d, %d)",
			t.LinesLow, t.LinesMedium, t.LinesHigh,
		))
	}

	for i, p := range c.ForbiddenPaths {
		if strings.TrimSpace(p) == "" {
			errs = append(errs, fmt.Sprintf("forbidden_paths[%d]: empty pattern", i))
		}
	}
	for i, p := range c.TestFileGlobs {
		if strings.TrimSpace(p) == "" {
			errs = append(errs, fmt.Sprintf("test_file_globs[%d]: empty pattern", i))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("invalid config:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

const defaultTemplate = `# AgentGrid configuration. Edit to taste; re-run ` + "`agentgrid init`" + ` is safe.

version: 1

# Branch agents are based on by default. Override per agent with --base.
default_base_branch: %s

# Paths flagged as "forbidden" in diff-risk reports. Touching one of these
# contributes a HIGH-severity reason.
forbidden_paths:
  - vendor/**
  - node_modules/**
  - migrations/**
  - infra/prod/**

# Globs used to detect test files when evaluating whether a code change
# ships with tests.
test_file_globs:
  - "**/*_test.go"
  - "**/test/**"
  - "**/__tests__/**"

# Thresholds for diff-risk scoring. Must satisfy low < medium < high.
diff_risk:
  thresholds:
    files_low: 5
    files_medium: 15
    files_high: 30
    lines_low: 200
    lines_medium: 600
    lines_high: 1500
`
