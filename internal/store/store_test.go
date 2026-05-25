package store

import (
	"path/filepath"
	"testing"
)

func TestOpenAppliesMigrations(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	wantTables := []string{
		"schema_migrations",
		"agents",
		"claims",
		"diff_snapshots",
		"stale_marks",
	}
	for _, name := range wantTables {
		var got string
		err := s.DB().QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`,
			name,
		).Scan(&got)
		if err != nil {
			t.Errorf("table %s missing: %v", name, err)
		}
	}

	var version int
	if err := s.DB().QueryRow(
		`SELECT MAX(version) FROM schema_migrations`,
	).Scan(&version); err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	if version != 1 {
		t.Errorf("schema version = %d, want 1", version)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")

	for i := 0; i < 3; i++ {
		s, err := Open(dbPath)
		if err != nil {
			t.Fatalf("Open #%d: %v", i, err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("Close #%d: %v", i, err)
		}
	}

	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("final Open: %v", err)
	}
	defer s.Close()

	var count int
	if err := s.DB().QueryRow(
		`SELECT COUNT(*) FROM schema_migrations`,
	).Scan(&count); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if count != 1 {
		t.Errorf("schema_migrations rows = %d, want 1", count)
	}
}
