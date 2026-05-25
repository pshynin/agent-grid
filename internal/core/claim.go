package core

import "time"

type ClaimKind string

const (
	ClaimKindPath ClaimKind = "path"
	ClaimKindGlob ClaimKind = "glob"
)

type ClaimIntent string

const (
	ClaimIntentEdit ClaimIntent = "edit"
	ClaimIntentRead ClaimIntent = "read"
)

type Claim struct {
	ID        string
	AgentID   string
	Kind      ClaimKind
	Pattern   string
	Intent    ClaimIntent
	CreatedAt time.Time
}
