// Package config loads runtime configuration from environment variables.
//
// Twelve-factor style: all configuration comes from the environment so the same
// binary can run unchanged across local, staging and production. A .env file is
// loaded automatically (for local dev) but never overrides variables already
// present in the real environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration for the server.
type Config struct {
	// Server
	Env             string
	Host            string
	Port            string
	LogLevel        string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	RequestTimeout  time.Duration // per-request handler deadline
	ShutdownTimeout time.Duration

	// CORS
	CORSAllowedOrigins []string

	// Database (PostgreSQL — works with hosted providers like Neon, Supabase, RDS)
	DatabaseURL        string
	DBMaxConns         int32
	DBMinConns         int32
	DBMaxConnLifetime  time.Duration
	DBMaxConnIdleTime  time.Duration
	DBConnectTimeout   time.Duration
	DBHealthCheckEvery time.Duration
}

// Load reads configuration from the environment (after loading any .env file),
// applying sensible production defaults for anything not set. It returns an
// error only for invalid required values.
func Load() (Config, error) {
	loadDotEnv(".env")

	cfg := Config{
		Env:             getEnv("APP_ENV", "development"),
		Host:            getEnv("HOST", "0.0.0.0"),
		Port:            getEnv("PORT", "8080"),
		LogLevel:        getEnv("LOG_LEVEL", "info"),
		ReadTimeout:     getDuration("READ_TIMEOUT", 10*time.Second),
		WriteTimeout:    getDuration("WRITE_TIMEOUT", 15*time.Second),
		IdleTimeout:     getDuration("IDLE_TIMEOUT", 60*time.Second),
		RequestTimeout:  getDuration("REQUEST_TIMEOUT", 30*time.Second),
		ShutdownTimeout: getDuration("SHUTDOWN_TIMEOUT", 15*time.Second),

		CORSAllowedOrigins: getCSV("CORS_ALLOWED_ORIGINS", []string{"*"}),

		DatabaseURL:        getEnv("DATABASE_URL", ""),
		DBMaxConns:         int32(getInt("DB_MAX_CONNS", 10)),
		DBMinConns:         int32(getInt("DB_MIN_CONNS", 0)),
		DBMaxConnLifetime:  getDuration("DB_MAX_CONN_LIFETIME", time.Hour),
		DBMaxConnIdleTime:  getDuration("DB_MAX_CONN_IDLE_TIME", 30*time.Minute),
		DBConnectTimeout:   getDuration("DB_CONNECT_TIMEOUT", 10*time.Second),
		DBHealthCheckEvery: getDuration("DB_HEALTH_CHECK_PERIOD", time.Minute),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required (e.g. a Neon connection string with sslmode=require)")
	}

	return cfg, nil
}

// Addr returns the host:port the server should bind to.
func (c Config) Addr() string { return fmt.Sprintf("%s:%s", c.Host, c.Port) }

// IsProduction reports whether the app is running in a production environment.
func (c Config) IsProduction() bool { return c.Env == "production" }

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func getInt(key string, fallback int) int {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getCSV(key string, fallback []string) []string {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return fallback
	}
	return out
}

func getDuration(key string, fallback time.Duration) time.Duration {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return time.Duration(secs) * time.Second
	}
	return fallback
}

// loadDotEnv reads a simple KEY=VALUE file and sets any variables that are not
// already present in the environment. Missing file is not an error. Lines
// starting with # are comments; surrounding quotes on values are stripped.
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // no .env file — fine in production
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}
