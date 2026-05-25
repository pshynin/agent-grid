package core

import "time"

type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
)

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
	RiskReasons     []string
	TakenAt         time.Time
}
