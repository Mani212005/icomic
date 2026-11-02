package main

import (
	"context"
	"database/sql"
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	_ "github.com/lib/pq"
	"icomic/backend/internal/db"
)

func main() {
	var (
		dbURL      string
		dir        string
		applySeed  bool
		timeoutSec int
	)

	flag.StringVar(&dbURL, "database-url", "", "PostgreSQL connection string (defaults to DATABASE_URL env)")
	flag.StringVar(&dir, "dir", "", "Directory containing migrations (defaults to deploy/migrations)")
	flag.BoolVar(&applySeed, "seed", false, "Apply seeds.sql after migrations")
	flag.IntVar(&timeoutSec, "timeout", 60, "Migration timeout in seconds")
	flag.Parse()

	if dbURL == "" {
		dbURL = os.Getenv("DATABASE_URL")
	}
	if dbURL == "" {
		log.Fatal("DATABASE_URL environment variable or -database-url flag is required")
	}

	if dir == "" {
		dir = os.Getenv("MIGRATIONS_DIR")
	}
	if dir == "" {
		candidates := []string{
			"deploy/migrations",
			"../deploy/migrations",
			"../../deploy/migrations",
		}
		for _, c := range candidates {
			if info, err := os.Stat(c); err == nil && info.IsDir() {
				dir = c
				break
			}
		}
		if dir == "" {
			dir = "deploy/migrations"
		}
	}

	if !applySeed && (os.Getenv("APPLY_SEEDS") == "true" || os.Getenv("APPLY_SEEDS") == "1") {
		applySeed = true
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	sqlDB, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("Failed to open database connection: %v", err)
	}
	defer sqlDB.Close()

	if err := sqlDB.PingContext(ctx); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	runner := db.NewMigrationRunner(sqlDB, dir)
	log.Printf("Running migrations from %s...", dir)
	applied, err := runner.ApplyAll(ctx)
	if err != nil {
		log.Fatalf("Migration failed: %v", err)
	}

	if len(applied) == 0 {
		log.Println("Database schema is already up to date. No new migrations applied.")
	} else {
		log.Printf("Successfully applied %d migration(s): %v", len(applied), applied)
	}

	if applySeed {
		seedsPath := filepath.Join(filepath.Dir(dir), "seeds.sql")
		log.Printf("Applying seeds from %s...", seedsPath)
		if err := runner.ApplySeeds(ctx, seedsPath); err != nil {
			log.Fatalf("Failed to apply seeds: %v", err)
		}
		log.Println("Seeds applied successfully.")
	}
}
