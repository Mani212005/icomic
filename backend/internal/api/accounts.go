package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"icomic/backend/internal/accounts"
	"icomic/backend/internal/auth"
	"icomic/backend/internal/domain"
)

const maxAccountBodyBytes = 8 << 10

func writeCodedError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, JSONResponse{Code: code, Message: message})
}

func writeProjectLimit(w http.ResponseWriter, limit *domain.ProjectLimitError) {
	message := "Project limit reached."
	if limit.Kind != domain.AccountRegistered {
		message = "Guests can keep one project. Sign in or sign up to create more."
	}
	writeJSON(w, http.StatusForbidden, JSONResponse{
		Code: domain.ProjectLimitReachedCode, Message: message,
		Details: map[string]interface{}{"account_kind": string(limit.Kind), "limit": limit.Limit},
	})
}

type accountJSON struct {
	Kind         string  `json:"kind"`
	Email        *string `json:"email"`
	ProjectCount int     `json:"project_count"`
	ProjectLimit *int    `json:"project_limit"`
	CSRFToken    string  `json:"csrf_token,omitempty"`
}

type accountResponse struct {
	Account         *accountJSON `json:"account"`
	ClaimedProjects int          `json:"claimed_projects,omitempty"`
}

func (s *Server) accountResponse(r *http.Request, id auth.Identity, claimed int) (accountResponse, error) {
	view, err := s.Accounts.Account(r.Context(), id)
	if err != nil {
		return accountResponse{}, err
	}
	out := &accountJSON{Kind: string(view.Kind), ProjectCount: view.ProjectCount, ProjectLimit: view.ProjectLimit, CSRFToken: view.CSRFToken}
	if view.Email != "" {
		out.Email = &view.Email
	}
	return accountResponse{Account: out, ClaimedProjects: claimed}, nil
}

func (s *Server) registerAccountRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/auth/me", s.AccountMe)
	mux.HandleFunc("POST /api/v1/auth/guest", s.AccountGuest)
	mux.HandleFunc("POST /api/v1/auth/signup", s.AccountSignUp)
	mux.HandleFunc("POST /api/v1/auth/signin", s.AccountSignIn)
	mux.HandleFunc("POST /api/v1/auth/signout", s.AccountSignOut)
}

func (s *Server) originAllowed(origin string) bool {
	allowed := s.AllowedOrigin
	if allowed == "" {
		allowed = "http://localhost:5173"
	}
	for _, configured := range strings.Split(allowed, ",") {
		configured = strings.TrimSpace(configured)
		if configured == "*" || configured == origin {
			return true
		}
	}
	return false
}

// checkOrigin rejects browser requests from other origins. Requests without Origin are not from a browser.
func (s *Server) checkOrigin(w http.ResponseWriter, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowed(origin) {
		writeCodedError(w, http.StatusForbidden, "origin_not_allowed", "Request origin is not allowed.")
		return false
	}
	return true
}

// checkSessionCSRF requires the per-session token on state-changing requests that rely on the cookie.
// Header-authenticated callers (dev identity, bearer) carry no cookie, so they have nothing to forge.
func (s *Server) checkSessionCSRF(w http.ResponseWriter, r *http.Request, id auth.Identity) bool {
	if id.CSRFToken == "" {
		return true
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if !s.checkOrigin(w, r) {
		return false
	}
	given := r.Header.Get("X-CSRF-Token")
	if given == "" || subtle.ConstantTimeCompare([]byte(given), []byte(id.CSRFToken)) != 1 {
		writeCodedError(w, http.StatusForbidden, "csrf_invalid", "Missing or invalid CSRF token.")
		return false
	}
	return true
}

func clientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// cookieSecure keeps the Secure flag on everywhere except loopback hosts used for local development.
func cookieSecure(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return false
	}
	ip := net.ParseIP(host)
	return ip == nil || !ip.IsLoopback()
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, res *accounts.Result, now time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: auth.SessionCookieName, Value: res.Token, Path: "/",
		Expires: res.ExpiresAt, MaxAge: int(res.ExpiresAt.Sub(now).Seconds()),
		HttpOnly: true, Secure: cookieSecure(r), SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: auth.SessionCookieName, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: cookieSecure(r), SameSite: http.SameSiteLaxMode,
	})
}

// currentIdentity resolves the caller without failing, so account endpoints can serve anonymous visitors.
func (s *Server) currentIdentity(r *http.Request) (auth.Identity, bool) {
	id, err := auth.IdentityOf(s.Authenticator, r)
	return id, err == nil && id.UserID != ""
}

// currentSession resolves only a cookie session, never a header identity, for session-changing endpoints.
func (s *Server) currentSession(r *http.Request) (auth.Identity, bool) {
	id, err := auth.NewSessionAuthenticator(s.Accounts).AuthenticateIdentity(r)
	return id, err == nil
}

func (s *Server) writeAccountError(w http.ResponseWriter, err error) {
	var limited *accounts.RateLimitedError
	var email = accounts.ErrInvalidEmail
	switch {
	case errors.As(err, &limited):
		seconds := int(limited.RetryAfter.Seconds()) + 1
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		retry := float64(seconds)
		writeJSON(w, http.StatusTooManyRequests, JSONResponse{
			Code: accounts.RateLimitedCode, Message: "Too many attempts. Try again later.",
			Retryable: true, RetryAfterSeconds: &retry,
		})
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeCodedError(w, http.StatusUnauthorized, accounts.InvalidCredentialCode, "Incorrect email or password.")
	case errors.Is(err, email):
		writeCodedError(w, http.StatusBadRequest, "invalid_email", "Enter a valid email address.")
	case errors.Is(err, auth.ErrInvalidPassword):
		writeCodedError(w, http.StatusBadRequest, "weak_password", strings.TrimPrefix(err.Error(), auth.ErrInvalidPassword.Error()+": "))
	case errors.Is(err, accounts.ErrEmailTaken):
		writeCodedError(w, http.StatusConflict, "email_taken", "An account with this email already exists.")
	default:
		writeJSON(w, http.StatusInternalServerError, JSONResponse{Code: "INTERNAL_ERROR", Message: "Unable to complete the account request."})
	}
}

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func decodeCredentials(w http.ResponseWriter, r *http.Request) (credentials, bool) {
	var body credentials
	if mt := strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]); mt != "application/json" {
		writeCodedError(w, http.StatusUnsupportedMediaType, "invalid_request", "Send JSON.")
		return body, false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAccountBodyBytes))
	if err := dec.Decode(&body); err != nil || dec.More() {
		writeCodedError(w, http.StatusBadRequest, "invalid_request", "Invalid request payload.")
		return body, false
	}
	if _, err := dec.Token(); err != io.EOF {
		writeCodedError(w, http.StatusBadRequest, "invalid_request", "Invalid request payload.")
		return body, false
	}
	return body, true
}

// issueResponse sets the session cookie and answers with the account.
func (s *Server) issueResponse(w http.ResponseWriter, r *http.Request, res *accounts.Result, status int) {
	setSessionCookie(w, r, res, s.Accounts.Now().UTC())
	out, err := s.accountResponse(r, res.Identity, res.Claimed)
	if err != nil {
		s.writeAccountError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, status, out)
}

// AccountMe handles GET /api/v1/auth/me. Anonymous visitors get a null account, not an error.
func (s *Server) AccountMe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, ok := s.currentIdentity(r)
	if !ok {
		writeJSON(w, http.StatusOK, accountResponse{})
		return
	}
	out, err := s.accountResponse(r, id, 0)
	if err != nil {
		s.writeAccountError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// AccountGuest handles POST /api/v1/auth/guest. An existing session is returned as is.
func (s *Server) AccountGuest(w http.ResponseWriter, r *http.Request) {
	if !s.checkOrigin(w, r) {
		return
	}
	if id, ok := s.currentIdentity(r); ok {
		out, err := s.accountResponse(r, id, 0)
		if err != nil {
			s.writeAccountError(w, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, out)
		return
	}
	res, err := s.Accounts.StartGuest(r.Context(), clientIP(r))
	if err != nil {
		s.writeAccountError(w, err)
		return
	}
	s.issueResponse(w, r, res, http.StatusCreated)
}

// AccountSignUp handles POST /api/v1/auth/signup. A guest session is upgraded in place.
func (s *Server) AccountSignUp(w http.ResponseWriter, r *http.Request) {
	if !s.checkOrigin(w, r) {
		return
	}
	body, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	current, _ := s.currentSession(r)
	res, err := s.Accounts.SignUp(r.Context(), current, clientIP(r), body.Email, body.Password)
	if err != nil {
		s.writeAccountError(w, err)
		return
	}
	s.issueResponse(w, r, res, http.StatusCreated)
}

// AccountSignIn handles POST /api/v1/auth/signin. A guest session's project is claimed by the account.
func (s *Server) AccountSignIn(w http.ResponseWriter, r *http.Request) {
	if !s.checkOrigin(w, r) {
		return
	}
	body, ok := decodeCredentials(w, r)
	if !ok {
		return
	}
	current, _ := s.currentSession(r)
	res, err := s.Accounts.SignIn(r.Context(), current, clientIP(r), body.Email, body.Password)
	if err != nil {
		s.writeAccountError(w, err)
		return
	}
	s.issueResponse(w, r, res, http.StatusOK)
}

// AccountSignOut handles POST /api/v1/auth/signout and revokes the session on the server.
func (s *Server) AccountSignOut(w http.ResponseWriter, r *http.Request) {
	current, ok := s.currentSession(r)
	if ok && !s.checkSessionCSRF(w, r, current) {
		return
	}
	if ok {
		if err := s.Accounts.SignOut(r.Context(), current); err != nil {
			s.writeAccountError(w, err)
			return
		}
	}
	clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}
