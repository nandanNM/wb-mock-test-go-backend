// Command server is the entrypoint for the HTTP backend.
//
// It wires configuration, logging, the database, routes and middleware, then
// runs the server with graceful shutdown so in-flight requests finish and the
// DB pool is closed cleanly on SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"backend/internal/audit"
	"backend/internal/auth"
	"backend/internal/config"
	"backend/internal/database"
	"backend/internal/handler"
	"backend/internal/logger"
	"backend/internal/middleware"
	"backend/internal/rbac"
	"backend/internal/repository"
)

// version is overridable at build time:
//
//	go build -ldflags "-X main.version=$(git rev-parse --short HEAD)"
var version = "dev"

func main() {
	if err := run(); err != nil {
		logger.New("error", true).Error("server terminated", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// JSON logs in production, human-readable text locally.
	log := logger.New(cfg.LogLevel, cfg.IsProduction())

	// Root context cancelled on shutdown signal; propagates to DB operations.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// --- Database ---------------------------------------------------------
	pool, err := database.Connect(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := database.Migrate(ctx, pool, log); err != nil {
		return err
	}

	// --- Repositories -----------------------------------------------------
	userRepo := repository.NewUserRepository(pool)
	identityRepo := repository.NewIdentityRepository(pool)
	sessionRepo := repository.NewSessionRepository(pool)
	roleRepo := repository.NewRoleRepository(pool)
	auditRepo := repository.NewAuditRepository(pool)
	flowRepo := repository.NewOAuthFlowRepository(pool)

	// --- Auth wiring ------------------------------------------------------
	tokenSvc, err := auth.NewTokenService(cfg.Auth, log)
	if err != nil {
		return err
	}
	googleProvider, err := auth.NewGoogleProvider(ctx, cfg.Auth)
	if err != nil {
		return err
	}
	if googleProvider == nil {
		log.Warn("Google login disabled — set GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET to enable it")
	}

	auditRecorder := audit.New(auditRepo, log)
	transport := auth.NewTransport(cfg.Auth, log)

	// The status cache resolves account/session status from the DB on a miss
	// (the resolver depends only on the session repo, avoiding a bootstrap cycle).
	statusResolver := func(ctx context.Context, _, sessionID string) (auth.AccountStatus, error) {
		userStatus, revoked, err := sessionRepo.Status(ctx, sessionID)
		if err != nil {
			return auth.AccountStatus{}, err
		}
		return auth.AccountStatus{UserStatus: userStatus, SessionRevoked: revoked}, nil
	}
	statusCache := auth.NewMemStatusCache(cfg.Auth.StatusCacheTTL, statusResolver)

	authSvc := auth.NewService(auth.Deps{
		Users:               userRepo,
		Identities:          identityRepo,
		Sessions:            sessionRepo,
		Roles:               roleRepo,
		Tokens:              tokenSvc,
		Cache:               statusCache,
		Audit:               auditRecorder,
		Flows:               flowRepo,
		Google:              googleProvider,
		RefreshTTL:          cfg.Auth.RefreshTokenTTL,
		BootstrapAdminEmail: cfg.Auth.BootstrapAdminEmail,
		Log:                 log,
	})

	authn := middleware.NewAuthenticator(tokenSvc, statusCache)
	csrf := middleware.NewCSRF(transport)
	rbacSvc := rbac.NewService(roleRepo, time.Minute)

	readiness := func(ctx context.Context) error { return database.HealthCheck(ctx, pool) }
	api := handler.New(handler.Deps{
		Version:         version,
		Users:           userRepo,
		Readiness:       readiness,
		Auth:            authSvc,
		Transport:       transport,
		Authenticator:   authn,
		CSRF:            csrf,
		RBAC:            rbacSvc,
		SuccessRedirect: cfg.Auth.SuccessRedirectURL,
	})

	// --- Middleware -------------------------------------------------------
	// Order matters (outermost first): RequestID so every layer sees the ID;
	// Recover to catch panics; Logger to time the handler + inject the
	// request-scoped logger; then security headers, CORS and per-request timeout.
	h := middleware.Chain(
		api.Routes(),
		middleware.RequestID,
		middleware.Recover,
		middleware.Logger(log),
		middleware.SecurityHeaders,
		middleware.CORS(cfg.CORSAllowedOrigins),
		middleware.Timeout(cfg.RequestTimeout),
	)

	srv := &http.Server{
		Addr:         cfg.Addr(),
		Handler:      h,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	// Run the listener in a goroutine so main can wait for shutdown signals.
	serverErr := make(chan error, 1)
	go func() {
		log.Info("server starting", "addr", cfg.Addr(), "env", cfg.Env, "version", version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// Block until the context is cancelled (signal) or the server fails.
	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	// Graceful shutdown: stop accepting new connections and let in-flight
	// requests finish within the configured grace period.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	log.Info("shutting down", "timeout", cfg.ShutdownTimeout.String())
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed, forcing close", "error", err)
		_ = srv.Close()
		return err
	}

	log.Info("server stopped cleanly")
	return nil
}
