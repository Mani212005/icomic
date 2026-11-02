package api

import (
	"icomic/backend/gen"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/credits"
	"icomic/backend/internal/db"
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
		Repo:             repo,
		CreditService:    creditService,
		JobService:       jobService,
		AIClient:         aiClient,
		Authenticator:    authenticator,
		ImageHosting:     imageHosting,
		MaxPanelsPerPage: maxPanelsPerPage,
		DefaultUserQuota: 25.0,
	}
}
