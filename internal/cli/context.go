package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pshynin/agent-grid/internal/config"
	"github.com/pshynin/agent-grid/internal/git"
	"github.com/pshynin/agent-grid/internal/store"
)

type cmdContext struct {
	Repo  string
	Cfg   config.Config
	Store *store.Store
}

// openCmdContext locates the git repo containing cwd, loads the config, and
// opens the SQLite store. Callers must call (*cmdContext).Close when done.
func openCmdContext() (*cmdContext, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}
	repo, err := git.RepoRoot(cwd)
	if err != nil {
		return nil, err
	}
	cfgPath := config.Path(repo)
	if !fileExists(cfgPath) {
		return nil, fmt.Errorf("agentgrid is not initialized in %s (run 'agentgrid init' first)", repo)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	dbPath := filepath.Join(repo, ".agentgrid", "state.db")
	s, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	return &cmdContext{Repo: repo, Cfg: cfg, Store: s}, nil
}

func (c *cmdContext) Close() error {
	if c == nil || c.Store == nil {
		return nil
	}
	return c.Store.Close()
}
