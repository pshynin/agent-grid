package store

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type migration struct {
	version int
	name    string
}

func (s *Store) migrate() error {
	if _, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied, err := s.appliedVersions()
	if err != nil {
		return err
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if applied[m.version] {
			continue
		}
		if err := s.applyMigration(m); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) appliedVersions() (map[int]bool, error) {
	applied := map[int]bool{}
	rows, err := s.db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("read schema_migrations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	var ms []migration
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		var v int
		if _, err := fmt.Sscanf(e.Name(), "%04d_", &v); err != nil {
			return nil, fmt.Errorf("migration %q: filename must start with NNNN_: %w", e.Name(), err)
		}
		ms = append(ms, migration{version: v, name: e.Name()})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	return ms, nil
}

func (s *Store) applyMigration(m migration) error {
	data, err := fs.ReadFile(migrationsFS, "migrations/"+m.name)
	if err != nil {
		return fmt.Errorf("read %s: %w", m.name, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx for %s: %w", m.name, err)
	}
	if _, err := tx.Exec(string(data)); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("apply %s: %w", m.name, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`,
		m.version, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record %s: %w", m.name, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit %s: %w", m.name, err)
	}
	return nil
}
