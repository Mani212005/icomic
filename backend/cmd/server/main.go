package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"icomic/backend/gen"
	"icomic/backend/internal/api"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
	"icomic/backend/internal/jobs"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	var repo db.Repository
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL != "" {
		log.Printf("Connecting to PostgreSQL at %s...", dbURL)
		pgRepo, err := db.NewPostgresRepository(dbURL)
		if err != nil {
			log.Fatalf("Failed to initialize PostgreSQL repository: %v", err)
		}
		defer pgRepo.Close()
		repo = pgRepo
	} else {
		log.Println("Using in-memory repository fake...")
		repo = db.NewInMemoryRepository()
	}

	var aiClient *gen.AIServiceClient
	aiURL := os.Getenv("PYTHON_AI_URL")
	if aiURL != "" {
		aiClient = gen.NewAIServiceClient(aiURL, 30*time.Second)
	}

	// Pluggable policy seam
	// TODO(captain): image hosting provider (S3 / self-hosted / CDN) is a pending captain decision.
	imageHosting := auth.NewDefaultImageHosting(os.Getenv("IMAGE_BASE_URL"))

	// Authentication seam: in dev, allow dev auth only if DEV_AUTH_ENABLED=true
	devAuthEnabled := os.Getenv("DEV_AUTH_ENABLED") == "true"
	authenticator := auth.NewDevAuthenticator(devAuthEnabled, "00000000-0000-0000-0000-000000000001")

	creditSvc := credits.NewInMemoryCreditService(map[string]float64{
		"00000000-0000-0000-0000-000000000001": 100.0,
	})
	jobSvc := jobs.NewInMemoryJobService()

	maxPanelsPerPage := 4
	if mppStr := os.Getenv("MAX_PANELS_PER_PAGE"); mppStr != "" {
		if mpp, err := strconv.Atoi(mppStr); err == nil {
			maxPanelsPerPage = mpp
		}
	}

	server := api.NewServer(
		repo,
		creditSvc,
		jobSvc,
		aiClient,
		authenticator,
		imageHosting,
		maxPanelsPerPage,
	)
	if os.Getenv("KILL_SWITCH") == "true" || os.Getenv("KILL_SWITCH") == "1" {
		server.KillSwitchEnabled = true
	}

	handler := server.NewRouter()

	log.Printf("Starting Icomic Go API server on port %s...", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
