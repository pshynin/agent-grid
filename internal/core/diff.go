package core

import "time"

type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

type RiskSeverity string

const (
	SeverityLow    RiskSeverity = "low"
	SeverityMedium RiskSeverity = "medium"
	SeverityHigh   RiskSeverity = "high"
)

// Reason is one machine-readable entry in a diff-risk verdict. Codes are
// stable identifiers; Detail is a human-readable summary; Paths is the
// optional file list associated with the reason.
type Reason struct {
	Code     string       `json:"code"`
	Severity RiskSeverity `json:"severity"`
	Detail   string       `json:"detail"`
	Paths    []string     `json:"paths,omitempty"`
}

type DiffSnapshot struct {
	ID              string
	AgentID         string
	HeadCommit      string
	FilesChanged    int
	LinesAdded      int
	LinesRemoved    int
	TouchedFiles    []string
	ForbiddenHits   []string
	ClaimViolations []string
	RiskLevel       RiskLevel
	RiskReasons     []Reason
	TakenAt         time.Time
}
