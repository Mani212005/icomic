package api

import (
	"net/http"
	"time"

	"icomic/backend/gen"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
	"icomic/backend/internal/domain"
	"icomic/backend/internal/jobs"
)

// Server encapsulates HTTP handler state and dependencies (SPEC 8.3, ARCHITECTURE 2).
type Server struct {
	Repo              db.Repository
	CreditService     credits.CreditService
	JobService        jobs.JobService
	AIClient          *gen.AIServiceClient
	Authenticator     auth.Authenticator
	ImageHosting      auth.ImageHostingSeam
	KillSwitchEnabled bool
	KillSwitch        domain.KillSwitchProvider
	MaxPanelsPerPage  int
	DefaultUserQuota  float64
}

func NewServer(
	repo db.Repository,
	creditService credits.CreditService,
	jobService jobs.JobService,
	aiClient *gen.AIServiceClient,
	authenticator auth.Authenticator,
	imageHosting auth.ImageHostingSeam,
	maxPanelsPerPage int,
) *Server {
	if maxPanelsPerPage <= 0 {
		maxPanelsPerPage = 4
	}
	if authenticator == nil {
		authenticator = auth.NewDevAuthenticator(false, "")
	}
	if imageHosting == nil {
		imageHosting = auth.NewDefaultImageHosting("/media")
	}

	return &Server{
		Repo:              repo,
		CreditService:     creditService,
		JobService:        jobService,
		AIClient:          aiClient,
		Authenticator:     authenticator,
		ImageHosting:      imageHosting,
		KillSwitchEnabled: false,
		KillSwitch:        domain.NewInMemoryKillSwitchProvider(false, 0),
		MaxPanelsPerPage:  maxPanelsPerPage,
		DefaultUserQuota:  25.0,
	}
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

	return domain.CheckKillSwitchAndQuota(
		s.IsKillSwitchActive(),
		float64(dailyCount),
		s.DefaultUserQuota,
		float64(monthlyCount),
		s.DefaultUserQuota*10,
	)
}
