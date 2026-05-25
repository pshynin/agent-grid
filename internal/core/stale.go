package core

import "time"

type Recommendation string

const (
	RecommendRebase  Recommendation = "rebase"
	RecommendReplan  Recommendation = "re-plan"
	RecommendReview  Recommendation = "review"
	RecommendNarrow  Recommendation = "narrow"
)

type StaleMark struct {
	ID                string
	AgentID           string
	Reason            string
	ConflictingFiles  []string
	Recommendation    Recommendation
	CreatedAt         time.Time
}
