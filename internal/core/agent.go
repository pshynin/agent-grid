package core

import "time"

type Agent struct {
	ID           string
	Name         string
	Task         string
	Branch       string
	BaseBranch   string
	BaseCommit   string
	WorktreePath string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}
