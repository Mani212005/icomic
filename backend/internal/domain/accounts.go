package domain

import (
	"fmt"
	"time"
)

// AccountKind separates browser-bound guests from registered accounts.
// Any identity without a registered account is a guest.
type AccountKind string

const (
	AccountGuest      AccountKind = "guest"
	AccountRegistered AccountKind = "registered"
)

// ParseAccountKind maps stored or unknown values onto a kind. Unknown means guest.
func ParseAccountKind(raw string) AccountKind {
	if AccountKind(raw) == AccountRegistered {
		return AccountRegistered
	}
	return AccountGuest
}

// AccountPolicy holds the project limits. Guests are bound to this many projects;
// registered accounts may own up to the registered ceiling.
type AccountPolicy struct {
	GuestMaxProjects      int
	RegisteredMaxProjects int
}

func DefaultAccountPolicy() AccountPolicy {
	return AccountPolicy{GuestMaxProjects: 1, RegisteredMaxProjects: 100}
}

// ProjectAccess is what the authenticator seam says about the caller.
// Unlimited is only set for the configured dev identity and bypasses the project limit.
type ProjectAccess struct {
	Kind      AccountKind
	Unlimited bool
}

// Limit returns the project limit for the caller and whether a limit applies.
func (p AccountPolicy) Limit(access ProjectAccess) (int, bool) {
	if access.Unlimited {
		return 0, false
	}
	if access.Kind == AccountRegistered {
		return p.RegisteredMaxProjects, true
	}
	return p.GuestMaxProjects, true
}

// ProjectLimitError reports a refused project creation. The code is stable for clients.
type ProjectLimitError struct {
	Kind  AccountKind
	Limit int
}

const ProjectLimitReachedCode = "project_limit_reached"

func (e *ProjectLimitError) Error() string {
	return fmt.Sprintf("project limit reached: %s accounts may own %d project(s)", e.Kind, e.Limit)
}

// Session is a server-side login session. Only a hash of the cookie token is stored.
type Session struct {
	ID        string
	TokenHash []byte
	UserID    string
	CSRFToken string
	CreatedAt time.Time
	ExpiresAt time.Time
	RevokedAt *time.Time
}
