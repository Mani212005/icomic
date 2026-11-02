package db_test

import (
	"os"
	"path/filepath"
	"testing"

	"icomic/backend/internal/db"
)

func TestDiscoverUpMigrationsOrdering(t *testing.T) {
	tempDir := t.TempDir()

	// Create test migration files in unordered sequence
	testFiles := []string{
		"000003_add_feature.up.sql",
		"000003_add_feature.down.sql",
		"000001_initial_schema.up.sql",
		"000001_initial_schema.down.sql",
		"000002_add_index.up.sql",
		"000002_add_index.down.sql",
		"README.md",
		"notes.txt",
	}

	for _, fname := range testFiles {
		p := filepath.Join(tempDir, fname)
		if err := os.WriteFile(p, []byte("-- migration content"), 0644); err != nil {
			t.Fatalf("failed to write test file %s: %v", fname, err)
		}
	}

	runner := db.NewMigrationRunner(nil, tempDir)
	discovered, err := runner.DiscoverUpMigrations()
	if err != nil {
		t.Fatalf("DiscoverUpMigrations failed: %v", err)
	}

	if len(discovered) != 3 {
		t.Fatalf("expected 3 up migrations, got %d", len(discovered))
	}

	expectedOrder := []string{
		"000001_initial_schema",
		"000002_add_index",
		"000003_add_feature",
	}

	for i, exp := range expectedOrder {
		if discovered[i].Version != exp {
			t.Errorf("migration[%d]: expected version %q, got %q", i, exp, discovered[i].Version)
		}
		if discovered[i].Filename != exp+".up.sql" {
			t.Errorf("migration[%d]: expected filename %q, got %q", i, exp+".up.sql", discovered[i].Filename)
		}
	}
}

func TestDiscoverRealProjectMigrations(t *testing.T) {
	candidates := []string{
		"../../../deploy/migrations",
		"../../deploy/migrations",
		"../deploy/migrations",
		"deploy/migrations",
	}

	var foundDir string
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			foundDir = c
			break
		}
	}

	if foundDir == "" {
		t.Skip("deploy/migrations directory not found from test runner working directory")
	}

	runner := db.NewMigrationRunner(nil, foundDir)
	discovered, err := runner.DiscoverUpMigrations()
	if err != nil {
		t.Fatalf("DiscoverUpMigrations failed on %s: %v", foundDir, err)
	}

	if len(discovered) == 0 {
		t.Fatalf("expected at least 1 migration in %s, got 0", foundDir)
	}

	if discovered[0].Version != "000001_initial_schema" {
		t.Errorf("expected first migration to be 000001_initial_schema, got %q", discovered[0].Version)
	}

	content, err := os.ReadFile(discovered[0].Path)
	if err != nil || len(content) == 0 {
		t.Fatalf("failed to read migration file %s: %v", discovered[0].Path, err)
	}
}
