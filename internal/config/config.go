package config

import (
	"errors"
	"os"
	"strconv"
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
}

// Load reads configuration from environment variables with sensible defaults.
func Load() (Config, error) {
	c := Config{
		DatabaseURL:     getEnv("DATABASE_URL", "postgres://elyfeed:elyfeed@localhost:5432/elyfeed?sslmode=disable"),
		Host:            getEnv("HOST", "0.0.0.0"),
		Port:            envInt("PORT", 8080),
		RefreshInterval: envDuration("REFRESH_INTERVAL", 10*time.Minute),
		FeedUserAgent:   getEnv("FEED_USER_AGENT", "elyfeed/1.0 (+https://github.com/wgelyjr/elyfeed)"),
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required")
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
