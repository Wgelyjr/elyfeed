package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"

	"elyfeed/internal/store"
)

// SessionCookieName is the name of the session cookie.
const SessionCookieName = "elyfeed_session"

// Purpose values for email verification tokens.
const (
	purposeVerify = "verify"
	purposeReset  = "reset"
)

// Verification and state lifetimes.
const (
	verificationTTL  = 24 * time.Hour
	resetTTL         = time.Hour
	oidcStateTTL     = 10 * time.Minute
	ratePruneWindow  = time.Hour
	loginEmailLimit  = 5  // logins per email
	loginIPLimit     = 20 // logins per IP
	registerIPLimit  = 5  // registrations per IP
	forgotEmailLimit = 3  // reset requests per email
	window15min      = 15 * time.Minute
	window1h         = time.Hour
)

// Sentinel errors for auth operations. The HTTP layer maps these to status
// codes.
var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrUserNotVerified    = errors.New("email not verified")
	ErrRateLimited        = errors.New("too many attempts, please try again later")
	ErrInvalidToken       = errors.New("invalid or expired token")
	ErrInvalidEmail       = errors.New("invalid email address")
	ErrWeakPassword       = errors.New("password must be at least 8 characters")
	ErrOIDCDisabled       = errors.New("OIDC is not configured")
)

// dummyHash is verified when a login references an unknown email or an
// account without a password, so the response time does not reveal which
// case occurred.
var dummyHash = mustHash("elyfeed-timing-equalizer")

func mustHash(p string) string {
	h, err := HashPassword(p)
	if err != nil {
		panic(err)
	}
	return h
}

// Service implements registration, login, sessions, email verification and
// the OIDC login flow on top of the store.
type Service struct {
	store        store.Store
	mailer       Mailer
	oidc         OIDCClient
	log          *slog.Logger
	limiter      *RateLimiter
	baseURL      string
	sessionTTL   time.Duration
	cookieSecure bool
	now          func() time.Time

	mu     sync.Mutex
	states map[string]time.Time
}

// NewService builds an auth service. baseURL may be empty, in which case
// links are derived from the request host. oidcClient may be nil to disable
// the OIDC login flow.
func NewService(st store.Store, mailer Mailer, oidcClient OIDCClient, log *slog.Logger, baseURL string, sessionTTL time.Duration, cookieSecure bool) *Service {
	return &Service{
		store:        st,
		mailer:       mailer,
		oidc:         oidcClient,
		log:          log,
		limiter:      NewRateLimiter(),
		baseURL:      strings.TrimSuffix(baseURL, "/"),
		sessionTTL:   sessionTTL,
		cookieSecure: cookieSecure,
		now:          time.Now,
		states:       make(map[string]time.Time),
	}
}

// Start launches background maintenance (rate limiter cleanup). It returns
// immediately.
func (s *Service) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.limiter.Prune(ratePruneWindow)
			}
		}
	}()
}

// link builds an absolute URL for the given path, preferring the configured
// base URL and falling back to the request host.
func (s *Service) link(r *http.Request, path string) string {
	if s.baseURL != "" {
		return s.baseURL + path
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + path
}

// normalizeEmail lowercases and trims an email address.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// validEmail reports whether email parses as an address.
func validEmail(email string) bool {
	_, err := mail.ParseAddress(email)
	return err == nil
}

// Register creates an account and sends a verification email. If the email is
// already registered but not yet verified, a fresh verification email is
// resent instead of an error.
func (s *Service) Register(ctx context.Context, r *http.Request, email, name, password string) error {
	email = normalizeEmail(email)
	if !validEmail(email) {
		return ErrInvalidEmail
	}
	if len(password) < MinPasswordLen {
		return ErrWeakPassword
	}
	if !s.limiter.Allow("reg:ip:"+clientIP(r), registerIPLimit, window1h) {
		return ErrRateLimited
	}

	existing, err := s.store.GetUserByEmail(ctx, email)
	if err == nil {
		if existing.EmailVerified {
			return fmt.Errorf("%w: %s", store.ErrConflict, email)
		}
		// Unverified re-registration: resend the verification email.
		return s.sendVerification(ctx, r, email)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}

	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if name == "" {
		name = strings.Split(email, "@")[0]
	}
	if _, err := s.store.CreateUser(ctx, email, name, hash); err != nil {
		return err
	}
	return s.sendVerification(ctx, r, email)
}

// sendVerification issues a verification token and emails the link.
func (s *Service) sendVerification(ctx context.Context, r *http.Request, email string) error {
	token, err := NewToken()
	if err != nil {
		return err
	}
	expires := s.now().Add(verificationTTL)
	if err := s.store.CreateEmailVerification(ctx, email, HashToken(token), purposeVerify, expires); err != nil {
		return err
	}
	link := s.link(r, "/api/auth/verify?token="+url.QueryEscape(token))
	body := fmt.Sprintf(
		"Hello,\n\nPlease verify your email address to activate your elyfeed account:\n\n%s\n\nThis link expires in 24 hours.\n\nIf you did not register an account, you can ignore this email.\n",
		link,
	)
	if err := s.mailer.Send(email, "Verify your elyfeed account", body); err != nil {
		s.log.Error("send verification email", "email", email, "err", err)
		return fmt.Errorf("send verification email: %w", err)
	}
	return nil
}

// Login verifies credentials and creates a session. It returns the user and
// the raw session token (for the cookie).
func (s *Service) Login(ctx context.Context, r *http.Request, email, password string) (*store.User, string, error) {
	email = normalizeEmail(email)
	if !s.limiter.Allow("login:email:"+email, loginEmailLimit, window15min) {
		return nil, "", ErrRateLimited
	}
	if !s.limiter.Allow("login:ip:"+clientIP(r), loginIPLimit, window15min) {
		return nil, "", ErrRateLimited
	}

	user, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			VerifyPassword(password, dummyHash)
			return nil, "", ErrInvalidCredentials
		}
		return nil, "", err
	}
	if user.PasswordHash == "" || !VerifyPassword(password, user.PasswordHash) {
		return nil, "", ErrInvalidCredentials
	}
	if !user.EmailVerified {
		return nil, "", ErrUserNotVerified
	}

	raw, err := s.sessionFor(ctx, user.ID)
	if err != nil {
		return nil, "", err
	}
	return user, raw, nil
}

// newSession stores a session and returns the raw token and its hash.
func (s *Service) newSession(ctx context.Context, userID int64) (raw, token string, err error) {
	raw, err = NewToken()
	if err != nil {
		return "", "", err
	}
	token = HashToken(raw)
	if err := s.store.CreateSession(ctx, token, userID, s.now().Add(s.sessionTTL)); err != nil {
		return "", "", err
	}
	return raw, token, nil
}

// Logout deletes the session identified by the raw cookie token. It is safe
// to call with an unknown or empty token.
func (s *Service) Logout(ctx context.Context, rawToken string) {
	if rawToken == "" {
		return
	}
	if err := s.store.DeleteSession(ctx, HashToken(rawToken)); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Warn("delete session", "err", err)
	}
}

// VerifyEmail consumes a verification token, activates the user and creates a
// session. It returns the user and the raw session token.
func (s *Service) VerifyEmail(ctx context.Context, rawToken string) (*store.User, string, error) {
	email, ok, err := s.store.ConsumeEmailVerification(ctx, HashToken(rawToken), purposeVerify)
	if err != nil {
		return nil, "", err
	}
	if !ok {
		return nil, "", ErrInvalidToken
	}
	user, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		return nil, "", err
	}
	first, err := s.store.ActivateUser(ctx, user.ID)
	if err != nil {
		return nil, "", err
	}
	if first {
		s.log.Info("first user activated; legacy data reassigned", "user_id", user.ID)
	}
	raw, err2 := s.sessionFor(ctx, user.ID)
	if err2 != nil {
		return nil, "", err2
	}
	user.EmailVerified = true
	return user, raw, nil
}

// ForgotPassword emails a password reset link. It always reports success for
// an existing user to avoid leaking which emails are registered.
func (s *Service) ForgotPassword(ctx context.Context, r *http.Request, email string) error {
	email = normalizeEmail(email)
	if !validEmail(email) || !s.limiter.Allow("forgot:email:"+email, forgotEmailLimit, window1h) {
		return nil
	}
	user, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		// Unknown email: pretend success so the endpoint does not enumerate
		// accounts.
		return nil
	}
	token, err := NewToken()
	if err != nil {
		return err
	}
	expires := s.now().Add(resetTTL)
	if err := s.store.CreateEmailVerification(ctx, user.Email, HashToken(token), purposeReset, expires); err != nil {
		return err
	}
	link := s.link(r, "/reset-password?token="+url.QueryEscape(token))
	body := fmt.Sprintf(
		"Hello,\n\nWe received a request to reset the password of your elyfeed account. Follow the link below to choose a new one:\n\n%s\n\nThis link expires in one hour.\n\nIf you did not request a reset, you can ignore this email.\n",
		link,
	)
	if err := s.mailer.Send(user.Email, "Reset your elyfeed password", body); err != nil {
		s.log.Error("send reset email", "email", user.Email, "err", err)
		return fmt.Errorf("send reset email: %w", err)
	}
	return nil
}

// ResetPassword consumes a reset token and sets a new password.
func (s *Service) ResetPassword(ctx context.Context, rawToken, password string) error {
	if len(password) < MinPasswordLen {
		return ErrWeakPassword
	}
	email, ok, err := s.store.ConsumeEmailVerification(ctx, HashToken(rawToken), purposeReset)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidToken
	}
	user, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		return err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return s.store.SetUserPasswordHash(ctx, user.ID, hash)
}

// OIDCAuthURL starts the OIDC login flow: it records a fresh state token and
// returns the provider URL to redirect the browser to.
func (s *Service) OIDCAuthURL() (string, error) {
	if s.oidc == nil {
		return "", ErrOIDCDisabled
	}
	token, err := NewToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	// Expire old states while we are at it.
	cutoff := s.now().Add(-oidcStateTTL)
	for k, exp := range s.states {
		if exp.Before(cutoff) {
			delete(s.states, k)
		}
	}
	s.states[token] = s.now().Add(oidcStateTTL)
	s.mu.Unlock()
	return s.oidc.AuthURL(token), nil
}

// OIDCCallback finishes the OIDC login flow: it validates the state,
// exchanges the code, provisions the user on first sight and creates a
// session. It returns the user and the raw session token.
func (s *Service) OIDCCallback(ctx context.Context, code, state string) (*store.User, string, error) {
	if s.oidc == nil {
		return nil, "", ErrOIDCDisabled
	}
	s.mu.Lock()
	exp, ok := s.states[state]
	delete(s.states, state)
	s.mu.Unlock()
	if !ok || s.now().After(exp) {
		return nil, "", ErrInvalidToken
	}

	email, name, err := s.oidc.Exchange(ctx, code)
	if err != nil {
		return nil, "", err
	}
	email = normalizeEmail(email)

	user, err := s.store.GetUserByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			return nil, "", err
		}
		user, err = s.store.CreateUser(ctx, email, name, "")
		if err != nil {
			return nil, "", err
		}
	}

	// The provider already verified the email (checked in Exchange), so this
	// first login activates the account — which also grants admin and legacy
	// data to the very first user.
	if !user.EmailVerified {
		first, err := s.store.ActivateUser(ctx, user.ID)
		if err != nil {
			return nil, "", err
		}
		if first {
			s.log.Info("first user activated via OIDC; legacy data reassigned", "user_id", user.ID)
		}
		user.EmailVerified = true
	}

	raw, err := s.sessionFor(ctx, user.ID)
	if err != nil {
		return nil, "", err
	}
	return user, raw, nil
}

func (s *Service) sessionFor(ctx context.Context, userID int64) (string, error) {
	raw, _, err := s.newSession(ctx, userID)
	return raw, err
}

// Middleware resolves the session cookie and, when it is valid, puts the
// authenticated user's ID in the request context. Requests without a valid
// session pass through unauthenticated; endpoints decide whether to require
// one.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(SessionCookieName)
		if err == nil && c.Value != "" {
			sess, err := s.store.GetSession(r.Context(), HashToken(c.Value))
			if err == nil {
				if s.now().Before(sess.ExpiresAt) {
					if user, err := s.store.GetUser(r.Context(), sess.UserID); err == nil {
						next.ServeHTTP(w, r.WithContext(WithUserID(r.Context(), user.ID)))
						return
					}
				} else {
					_ = s.store.DeleteSession(r.Context(), sess.TokenHash)
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// SetSessionCookie writes the session cookie carrying rawToken.
func (s *Service) SetSessionCookie(w http.ResponseWriter, rawToken string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    rawToken,
		Path:     "/",
		MaxAge:   int(s.sessionTTL.Seconds()),
		Expires:  s.now().Add(s.sessionTTL),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure,
	})
}

// ClearSessionCookie expires the session cookie.
func (s *Service) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.cookieSecure,
	})
}

// MinPasswordLen is the minimum accepted password length.
const MinPasswordLen = 8

// clientIP extracts the peer IP from RemoteAddr. It deliberately ignores
// X-Forwarded-For: elyfeed is deployed behind a terminating proxy that is
// trusted to do its own abuse control, and spoofing the header would
// otherwise fragment rate limit keys.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
