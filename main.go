// Command elyfeed runs the RSS reader: Postgres-backed API and embedded web UI.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"elyfeed/internal/auth"
	"elyfeed/internal/config"
	"elyfeed/internal/db"
	"elyfeed/internal/refresh"
	"elyfeed/internal/rss"
	"elyfeed/internal/server"
	"elyfeed/internal/store"
	"elyfeed/internal/web"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	st := store.NewPostgres(pool)

	client := &http.Client{Timeout: 30 * time.Second}
	// Feeds are user-supplied URLs, so they use a client with the SSRF guard;
	// the plain client is reserved for admin-configured endpoints (OIDC).
	feedClient := rss.NewClient(30*time.Second, cfg.FeedAllowPrivate)
	refresher := refresh.New(st, feedClient, cfg.FeedUserAgent, cfg.RefreshInterval, log)
	refresher.Start(ctx)

	assets, err := web.FS()
	if err != nil {
		return err
	}

	authService, err := buildAuthService(ctx, st, client, cfg, log)
	if err != nil {
		return err
	}
	authService.Start(ctx)

	handler := server.New(st, refresher, assets, authService)

	addr := cfg.Host + ":" + strconv.Itoa(cfg.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("elyfeed listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// buildAuthService wires the mailer, the OIDC client (when configured) and
// the auth service from the loaded configuration.
func buildAuthService(ctx context.Context, st store.Store, client *http.Client, cfg config.Config, log *slog.Logger) (*auth.Service, error) {
	var mailer auth.Mailer
	if cfg.SMTPHost != "" {
		mailer = auth.NewSMTPMailer(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom, cfg.SMTPImplicitTLS)
	} else {
		log.Warn("SMTP_HOST not set; verification emails will be printed to the log")
		mailer = auth.NewConsoleMailer(log)
	}

	var oidcClient auth.OIDCClient
	if cfg.OIDCIssuer != "" {
		discoverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		redirectURL := cfg.BaseURL + "/api/auth/oidc/callback"
		var err error
		oidcClient, err = auth.NewOIDCClient(discoverCtx, client, cfg.OIDCIssuer, cfg.OIDCClientID, cfg.OIDCClientSecret, redirectURL, cfg.OIDCScopes)
		if err != nil {
			return nil, fmt.Errorf("configure OIDC: %w", err)
		}
		log.Info("OIDC login enabled", "issuer", cfg.OIDCIssuer)
	}

	cookieSecure := strings.HasPrefix(cfg.BaseURL, "https://")
	return auth.NewService(st, mailer, oidcClient, log, cfg.BaseURL, cfg.SessionTTL, cookieSecure), nil
}
