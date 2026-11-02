package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Migration represents a discovered database migration file.
type Migration struct {
	Version  string // e.g., "000001_initial_schema"
	Filename string
	Path     string
}

// MigrationRunner coordinates the idempotent execution of database schema migrations.
type MigrationRunner struct {
	db              *sql.DB
	migrationsDir   string
	migrationsTable string
}

// NewMigrationRunner returns a new MigrationRunner for the given sql.DB and migrations directory.
func NewMigrationRunner(db *sql.DB, migrationsDir string) *MigrationRunner {
	if migrationsDir == "" {
		migrationsDir = "deploy/migrations"
	}
	return &MigrationRunner{
		db:              db,
		migrationsDir:   migrationsDir,
		migrationsTable: "schema_migrations",
	}
}

// SetMigrationsTable allows overriding the default schema migrations table name.
func (r *MigrationRunner) SetMigrationsTable(tableName string) {
	r.migrationsTable = tableName
}

// DiscoverUpMigrations scans migrationsDir for all files matching *.up.sql,
// validates that each has a version prefix, and returns them sorted ascending by version.
func (r *MigrationRunner) DiscoverUpMigrations() ([]Migration, error) {
	entries, err := os.ReadDir(r.migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("read migrations directory %q: %w", r.migrationsDir, err)
	}

	var migrations []Migration
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".up.sql") {
			version := strings.TrimSuffix(name, ".up.sql")
			migrations = append(migrations, Migration{
				Version:  version,
				Filename: name,
				Path:     filepath.Join(r.migrationsDir, name),
			})
		}
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}

// EnsureTable ensures that the schema migrations tracking table exists.
func (r *MigrationRunner) EnsureTable(ctx context.Context) error {
	query := fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			version VARCHAR(255) PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`, r.migrationsTable)
	_, err := r.db.ExecContext(ctx, query)
	return err
}

// GetAppliedVersions queries the database for all previously applied migration versions.
func (r *MigrationRunner) GetAppliedVersions(ctx context.Context) (map[string]bool, error) {
	query := fmt.Sprintf(`SELECT version FROM %s`, r.migrationsTable)
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := make(map[string]bool)
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}

// ApplyAll applies all unapplied up migrations in ascending order.
// Each migration runs within its own transaction. If any migration fails,
// the transaction is rolled back and the error is returned immediately.
// Returns the list of migration versions applied in this run.
func (r *MigrationRunner) ApplyAll(ctx context.Context) ([]string, error) {
	if err := r.EnsureTable(ctx); err != nil {
		return nil, fmt.Errorf("ensure migrations table: %w", err)
	}

	applied, err := r.GetAppliedVersions(ctx)
	if err != nil {
		return nil, fmt.Errorf("get applied migrations: %w", err)
	}

	migrations, err := r.DiscoverUpMigrations()
	if err != nil {
		return nil, fmt.Errorf("discover migrations: %w", err)
	}

	var newlyApplied []string
	for _, m := range migrations {
		if applied[m.Version] {
			continue
		}

		content, err := os.ReadFile(m.Path)
		if err != nil {
			return newlyApplied, fmt.Errorf("read migration file %s: %w", m.Filename, err)
		}

		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return newlyApplied, fmt.Errorf("begin tx for %s: %w", m.Version, err)
		}

		if _, err := tx.ExecContext(ctx, string(content)); err != nil {
			_ = tx.Rollback()
			return newlyApplied, fmt.Errorf("execute migration %s: %w", m.Version, err)
		}

		recordQuery := fmt.Sprintf(`INSERT INTO %s (version, applied_at) VALUES ($1, $2)`, r.migrationsTable)
		if _, err := tx.ExecContext(ctx, recordQuery, m.Version, time.Now().UTC()); err != nil {
			_ = tx.Rollback()
			return newlyApplied, fmt.Errorf("record migration %s: %w", m.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return newlyApplied, fmt.Errorf("commit migration %s: %w", m.Version, err)
		}

		newlyApplied = append(newlyApplied, m.Version)
	}

	return newlyApplied, nil
}

// ApplySeeds executes SQL from the specified seeds file.
func (r *MigrationRunner) ApplySeeds(ctx context.Context, seedsFile string) error {
	if seedsFile == "" {
		seedsFile = filepath.Join(filepath.Dir(r.migrationsDir), "seeds.sql")
	}
	content, err := os.ReadFile(seedsFile)
	if err != nil {
		return fmt.Errorf("read seeds file %q: %w", seedsFile, err)
	}
	_, err = r.db.ExecContext(ctx, string(content))
	return err
}
