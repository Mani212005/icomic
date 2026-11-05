package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"icomic/backend/gen"
	"icomic/backend/internal/api"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

func main() {
	imageLimits, err := domain.E2EImageQuota(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	var repo db.Repository
	var creditSvc credits.CreditService
	var jobSvc jobs.JobService
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL != "" {
		log.Println("Connecting to PostgreSQL...")
		pgRepo, err := db.NewPostgresRepository(dbURL)
		if err != nil {
			log.Fatalf("Failed to initialize PostgreSQL repository: %v", err)
		}
		defer pgRepo.Close()
		pgRepo.E2EImageQuota = imageLimits
		repo = pgRepo
		creditSvc = credits.NewPersistentCreditService(pgRepo)
		jobSvc = jobs.NewPersistentJobService(pgRepo)
	} else {
		log.Println("Using in-memory repository fake...")
		repo = db.NewInMemoryRepository()
		creditSvc = credits.NewInMemoryCreditService(map[string]float64{"00000000-0000-0000-0000-000000000001": 100.0})
		jobSvc = jobs.NewInMemoryJobService()
	}

	var aiClient gen.AIService
	aiURL := os.Getenv("PYTHON_AI_URL")
	if aiURL != "" {
		log.Printf("Connecting to Python AI service at %s", aiURL)
		aiClient = gen.NewAIServiceClient(aiURL, 16*time.Minute)
	} else {
		log.Println("PYTHON_AI_URL not set; using fake AI service client")
		aiClient = gen.NewFakeAIServiceClient()
	}

	// Pluggable policy seam
	// TODO(captain): image hosting provider (S3 / self-hosted / CDN) is a pending captain decision.
	imageHosting := auth.NewDefaultImageHosting(os.Getenv("IMAGE_BASE_URL"))

	// Authentication seam: in dev, allow dev auth only if DEV_AUTH_ENABLED=true
	devAuthEnabled := os.Getenv("DEV_AUTH_ENABLED") == "true"
	authenticator := auth.NewDevAuthenticator(devAuthEnabled, "00000000-0000-0000-0000-000000000001")

	maxPanelsPerPage := 5
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
	if imageLimits != nil {
		server.DefaultUserQuota = float64(imageLimits.Daily)
		server.MonthlyUserQuota = float64(imageLimits.Monthly)
		log.Printf("Isolated E2E image limits: daily=%d monthly=%d; persisted policy and counters unchanged", imageLimits.Daily, imageLimits.Monthly)
	}
	if os.Getenv("KILL_SWITCH") == "true" || os.Getenv("KILL_SWITCH") == "1" {
		server.KillSwitchEnabled = true
	}

	limits, err := api.RequestLimitsFromEnv(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	server.Limits = limits
	handler := server.NewRouter()
	httpServer, err := publicHTTPServer(":"+port, handler, os.Getenv)
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("Starting Icomic Go API server on port %s...", port)
	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
