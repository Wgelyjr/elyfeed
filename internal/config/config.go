package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds runtime configuration loaded from the environment.
type Config struct {
	// DatabaseURL is a Postgres DSN, e.g.
	// postgres://elyfeed:elyfeed@localhost:5432/elyfeed?sslmode=disable
	DatabaseURL string

	// Host is the address the HTTP server binds to.
	Host string

	// Port is the TCP port the HTTP server listens on.
	Port int

	// RefreshInterval is how often feeds are re-fetched in the background.
	// A zero value disables background refresh.
	RefreshInterval time.Duration

	// FeedUserAgent is the User-Agent sent when fetching feeds.
	FeedUserAgent string

	// BaseURL is the canonical public origin of the app, e.g.
	// https://feeds.example.com (no trailing slash). It is used to build
	// absolute links in email and the OIDC redirect URL. When empty, the
	// request Host header is used (local development).
	BaseURL string

	// SessionTTL is how long a login session stays valid.
	SessionTTL time.Duration

	// Dev relaxes production requirements (ELYFEED_DEV=true): it permits
	// starting without SMTP or OIDC configured, in which case verification
	// emails are printed to the log instead of sent.
	Dev bool

	// SMTP settings for sending verification and reset emails.
	SMTPHost        string
	SMTPPort        int
	SMTPUser        string
	SMTPPass        string
	SMTPFrom        string
	SMTPImplicitTLS bool

	// OIDC settings. When OIDCIssuer is empty, OIDC login is disabled.
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCScopes       []string

	// FeedAllowPrivate permits subscribing to feed URLs that resolve to
	// private or loopback addresses (development convenience; a SSRF risk
	// when exposed to the public internet).
	FeedAllowPrivate bool
}

// Load reads configuration from environment variables with sensible defaults.
func Load() (Config, error) {
	c := Config{
		DatabaseURL:      getEnv("DATABASE_URL", "postgres://elyfeed:elyfeed@localhost:5432/elyfeed?sslmode=disable"),
		Host:             getEnv("HOST", "0.0.0.0"),
		Port:             envInt("PORT", 8080),
		RefreshInterval:  envDuration("REFRESH_INTERVAL", 10*time.Minute),
		FeedUserAgent:    getEnv("FEED_USER_AGENT", "elyfeed/1.0 (+https://github.com/wgelyjr/elyfeed)"),
		BaseURL:          strings.TrimSuffix(os.Getenv("BASE_URL"), "/"),
		SessionTTL:       envDuration("SESSION_TTL", 30*24*time.Hour),
		Dev:              os.Getenv("ELYFEED_DEV") == "true",
		SMTPHost:         os.Getenv("SMTP_HOST"),
		SMTPPort:         envInt("SMTP_PORT", 587),
		SMTPUser:         os.Getenv("SMTP_USER"),
		SMTPPass:         os.Getenv("SMTP_PASS"),
		SMTPFrom:         getEnv("SMTP_FROM", "elyfeed <no-reply@localhost>"),
		SMTPImplicitTLS:  os.Getenv("SMTP_IMPLICIT_TLS") == "true",
		OIDCIssuer:       os.Getenv("OIDC_ISSUER"),
		OIDCClientID:     os.Getenv("OIDC_CLIENT_ID"),
		OIDCClientSecret: os.Getenv("OIDC_CLIENT_SECRET"),
		OIDCScopes:       envCSV("OIDC_SCOPES", "openid email profile"),
		FeedAllowPrivate: os.Getenv("FEED_ALLOW_PRIVATE") == "true",
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
	}
	if c.SessionTTL <= 0 {
		return c, errors.New("SESSION_TTL must be positive")
	}
	if c.SMTPHost == "" && c.OIDCIssuer == "" && !c.Dev {
		return c, errors.New("SMTP_HOST or OIDC_ISSUER must be configured (or set ELYFEED_DEV=true for local development)")
	}
	if c.OIDCIssuer != "" && (c.OIDCClientID == "" || c.OIDCClientSecret == "") {
		return c, errors.New("OIDC_ISSUER requires OIDC_CLIENT_ID and OIDC_CLIENT_SECRET")
	}
	if c.OIDCIssuer != "" && c.BaseURL == "" {
		return c, errors.New("OIDC_ISSUER requires BASE_URL (the OIDC redirect URL must be absolute)")
	}
	return c, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return fallback
}

// envCSV reads a comma-separated list from the environment, falling back to
// the provided default (also comma-separated) when unset.
func envCSV(key, fallback string) []string {
	v := os.Getenv(key)
	if v == "" {
		v = fallback
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
