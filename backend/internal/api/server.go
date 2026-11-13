package api

import (
	"net/http"
	"os"
	"sync"
	"time"

	"icomic/backend/gen"
	"icomic/backend/internal/accounts"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

// Server encapsulates HTTP handler state and dependencies (SPEC 8.3, ARCHITECTURE 2).
type Server struct {
	LetteringOverlayEnabled bool
	Repo                    db.Repository
	CreditService           credits.CreditService
	JobService              jobs.JobService
	Dispatches              db.DispatchStore // durable generation outbox; nil for the in-memory fakes
	AIClient                gen.AIService
	Authenticator           auth.Authenticator
	Accounts                *accounts.Service
	ImageHosting            auth.ImageHostingSeam
	KillSwitchEnabled       bool
	KillSwitch              domain.KillSwitchProvider
	MaxPanelsPerPage        int
	DefaultUserQuota        float64
	MonthlyUserQuota        float64
	AllowedOrigin           string
	PublicBaseURL           string // origin browsers use for media links; empty derives it from the request
	Limits                  RequestLimits
	Pricing                 credits.PricingConfig
	mediaSigner             *mediaSigner
	admission               requestAdmission
	poolOnce                sync.Once
	dispatchSlots           *dispatchPool
	dispatchWG              sync.WaitGroup
}

func NewServer(
	repo db.Repository,
	creditService credits.CreditService,
	jobService jobs.JobService,
	aiClient gen.AIService,
	authenticator auth.Authenticator,
	imageHosting auth.ImageHostingSeam,
	maxPanelsPerPage int,
) *Server {
	if maxPanelsPerPage <= 0 {
		maxPanelsPerPage = 5
	}
	if authenticator == nil {
		authenticator = auth.NewDevAuthenticator(false, "")
	}
	if imageHosting == nil {
		imageHosting = auth.NewDefaultImageHosting("/media")
	}
	if aiClient == nil {
		aiClient = gen.NewFakeAIServiceClient()
	}

	allowedOrigin := os.Getenv("CORS_ALLOWED_ORIGIN")
	if allowedOrigin == "" {
		allowedOrigin = "http://localhost:5173"
	}

	if memoryJobs, ok := jobService.(*jobs.InMemoryJobService); ok {
		memoryJobs.SetCreditService(creditService)
	}
	accountService := accounts.NewService(repo, nil)
	// Cookie sessions are tried first, then the configured authenticator (dev identity or custom).
	authenticator = auth.NewCompositeAuthenticator(auth.NewSessionAuthenticator(accountService), authenticator)
	server := &Server{
		Repo:                    repo,
		LetteringOverlayEnabled: os.Getenv("LETTERING_OVERLAY_ENABLED") == "true",
		CreditService:           creditService,
		JobService:              jobService,
		AIClient:                aiClient,
		Authenticator:           authenticator,
		Accounts:                accountService,
		ImageHosting:            imageHosting,
		KillSwitchEnabled:       false,
		KillSwitch:              domain.NewInMemoryKillSwitchProvider(false, 0),
		MaxPanelsPerPage:        maxPanelsPerPage,
		DefaultUserQuota:        80.0,
		AllowedOrigin:           allowedOrigin,
		PublicBaseURL:           os.Getenv("MEDIA_PUBLIC_BASE_URL"),
		mediaSigner:             newMediaSigner(os.Getenv("MEDIA_SIGNING_SECRET")),
		Limits:                  DefaultRequestLimits(),
		Pricing:                 credits.DefaultPricing(),
	}
	// The in-memory job service keeps its own outbox; PostgreSQL deployments set Dispatches.
	if store, ok := jobService.(db.DispatchStore); ok {
		server.Dispatches = store
	}
	return server
}

// IsKillSwitchActive returns true if either the runtime kill switch provider or legacy boolean flag is enabled (SPEC 5.5).
func (s *Server) IsKillSwitchActive() bool {
	if s.KillSwitch != nil && s.KillSwitch.IsActive() {
		return true
	}
	return s.KillSwitchEnabled
}

// CheckKillSwitchAndQuota checks global spend protection and per-user image generation quotas (SPEC 5.5).
func (s *Server) CheckKillSwitchAndQuota(r *http.Request, userID string) error {
	// Durable policy and quota reads belong to CreateGeneration's transaction.
	// The memory path keeps its existing guard for hermetic API callers.
	if policy, ok := s.JobService.(interface{ EnforcesPolicyTransactionally() bool }); ok && policy.EnforcesPolicyTransactionally() {
		return nil
	}

	dailyCount, _ := s.Repo.GetUserDailyImageCount(r.Context(), userID, time.Now().UTC().Add(-24*time.Hour))
	monthlyCount, _ := s.Repo.GetUserMonthlyImageCount(r.Context(), userID, time.Now().UTC().Add(-30*24*time.Hour))

	// Also check CreditService usage history for in-memory / testing setups
	if s.CreditService != nil {
		if history, err := s.CreditService.GetUsageHistory(r.Context(), userID); err == nil {
			sinceDaily := time.Now().UTC().Add(-24 * time.Hour)
			sinceMonthly := time.Now().UTC().Add(-30 * 24 * time.Hour)
			creditDaily := 0
			creditMonthly := 0
			for _, entry := range history {
				if entry.TransactionType == "debit" {
					if entry.GenerationJobID != nil {
						job, err := s.JobService.GetJob(r.Context(), *entry.GenerationJobID)
						if err == nil && job.JobType != "character_reference" && job.JobType != "draft_sketch" && job.JobType != "final_line_art" {
							continue
						}
					}
					if entry.CreatedAt.After(sinceDaily) {
						creditDaily++
					}
					if entry.CreatedAt.After(sinceMonthly) {
						creditMonthly++
					}
				}
			}
			if creditDaily > dailyCount {
				dailyCount = creditDaily
			}
			if creditMonthly > monthlyCount {
				monthlyCount = creditMonthly
			}
		}
	}

	monthlyLimit := s.MonthlyUserQuota
	if monthlyLimit <= 0 {
		monthlyLimit = s.DefaultUserQuota * 10
	}
	return domain.CheckKillSwitchAndQuota(
		s.IsKillSwitchActive(),
		float64(dailyCount),
		s.DefaultUserQuota,
		float64(monthlyCount),
		monthlyLimit,
	)
}
