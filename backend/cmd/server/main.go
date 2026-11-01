package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

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

	killSwitch := os.Getenv("KILL_SWITCH_ENABLED") == "true"
	blockedCountries := []string{"IN"}
	if customBlocked := os.Getenv("BLOCKED_COUNTRIES"); customBlocked != "" {
		blockedCountries = strings.Split(customBlocked, ",")
	}

	minAge := 18
	if ageStr := os.Getenv("MIN_USER_AGE"); ageStr != "" {
		if parsed, err := strconv.Atoi(ageStr); err == nil && parsed > 0 {
			minAge = parsed
		}
	}

	initialBalances := map[string]float64{
		"00000000-0000-0000-0000-000000000001": 100.00,
	}

	server := &api.Server{
		Repo:              db.NewInMemoryRepository(),
		CreditService:     credits.NewInMemoryCreditService(initialBalances),
		JobService:        jobs.NewInMemoryJobService(),
		AgeVerifier:       auth.NewDefaultAgeVerifier(minAge),
		GeoBlocker:        auth.NewConfigurableGeoBlocker(blockedCountries),
		KillSwitchEnabled: killSwitch,
		MaxPanelsPerPage:  6,
		DefaultUserQuota:  50.00,
	}

	handler := server.NewRouter()

	log.Printf("Starting Icomic Go API server on port %s (kill_switch=%v, min_age=%d)...", port, killSwitch, minAge)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
