// Package database manages the PostgreSQL connection pool.
//
// It uses pgxpool, which is the recommended pooled driver for production
// PostgreSQL in Go. The connection string comes from DATABASE_URL, so it works
// transparently with hosted providers such as Neon, Supabase, RDS or
// CloudSQL — just paste their connection URL (include `sslmode=require`).
package database

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"backend/internal/config"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Connect parses DATABASE_URL, applies pool tuning from config, opens the pool
// and verifies connectivity with a Ping before returning. The caller owns the
// pool and must Close it on shutdown.
func Connect(ctx context.Context, cfg config.Config, log *slog.Logger) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}

	// Pool tuning. These can also be set as query params in the URL, but we make
	// them explicit env-driven knobs for clarity.
	poolCfg.MaxConns = cfg.DBMaxConns
	poolCfg.MinConns = cfg.DBMinConns
	poolCfg.MaxConnLifetime = cfg.DBMaxConnLifetime
	poolCfg.MaxConnIdleTime = cfg.DBMaxConnIdleTime
	poolCfg.HealthCheckPeriod = cfg.DBHealthCheckEvery
	poolCfg.ConnConfig.ConnectTimeout = cfg.DBConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	// Verify connectivity up front so the process fails fast on bad credentials
	// or an unreachable host instead of on the first request.
	pingCtx, cancel := context.WithTimeout(ctx, cfg.DBConnectTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	log.Info("database connected",
		"host", poolCfg.ConnConfig.Host,
		"database", poolCfg.ConnConfig.Database,
		"max_conns", poolCfg.MaxConns,
	)
	return pool, nil
}

// HealthCheck pings the database within a short timeout. Used by the readiness
// probe so orchestrators only route traffic when the DB is reachable.
func HealthCheck(ctx context.Context, pool *pgxpool.Pool) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return pool.Ping(ctx)
}
