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

	// Authentication
	Auth AuthConfig
}

// AuthConfig holds authentication/OAuth/token settings. Secrets are validated
// lazily by Auth.Validate so the server can boot for non-auth use in dev.
type AuthConfig struct {
	// Google OAuth (OIDC)
	GoogleClientID     string
	GoogleClientSecret string
	GoogleRedirectURL  string
	SuccessRedirectURL string // where the browser lands after a successful login

	// BootstrapAdminEmail, if set, grants the 'admin' role to the user with this
	// email on login — a convenient way to seed the first administrator.
	BootstrapAdminEmail string

	// Access-token signing (EdDSA / Ed25519)
	JWTPrivateKeyB64 string // base64 of a 64-byte ed25519 private key; empty => ephemeral dev key
	JWTKeyID         string
	JWTIssuer        string
	JWTAudience      string
	AccessTokenTTL   time.Duration
	RefreshTokenTTL  time.Duration

	// Cookies (browser clients)
	CookieDomain string
	CookieSecure bool

	// CSRF + status cache
	CSRFHMACKey    string
	StatusCacheTTL time.Duration
}

// loadAuth reads the auth-related environment variables.
func loadAuth() AuthConfig {
	return AuthConfig{
		GoogleClientID:      getEnv("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret:  getEnv("GOOGLE_CLIENT_SECRET", ""),
		GoogleRedirectURL:   getEnv("GOOGLE_REDIRECT_URL", "http://localhost:8080/v1/auth/google/callback"),
		SuccessRedirectURL:  getEnv("OAUTH_SUCCESS_REDIRECT", ""),
		BootstrapAdminEmail: getEnv("AUTH_BOOTSTRAP_ADMIN_EMAIL", ""),

		JWTPrivateKeyB64: getEnv("AUTH_JWT_PRIVATE_KEY", ""),
		JWTKeyID:         getEnv("AUTH_JWT_KEY_ID", "default"),
		JWTIssuer:        getEnv("AUTH_JWT_ISSUER", "backend"),
		JWTAudience:      getEnv("AUTH_JWT_AUDIENCE", "backend-api"),
		AccessTokenTTL:   getDuration("ACCESS_TOKEN_TTL", 10*time.Minute),
		RefreshTokenTTL:  getDuration("REFRESH_TOKEN_TTL", 720*time.Hour),

		CookieDomain: getEnv("AUTH_COOKIE_DOMAIN", ""),
		CookieSecure: getBool("AUTH_COOKIE_SECURE", true),

		CSRFHMACKey:    getEnv("CSRF_HMAC_KEY", ""),
		StatusCacheTTL: getDuration("STATUS_CACHE_TTL", 20*time.Second),
	}
}

// GoogleConfigured reports whether Google OAuth credentials are present.
func (a AuthConfig) GoogleConfigured() bool {
	return a.GoogleClientID != "" && a.GoogleClientSecret != ""
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

		Auth: loadAuth(),
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

func getBool(key string, fallback bool) bool {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
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
		val = parseDotEnvValue(strings.TrimSpace(val))
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}

// parseDotEnvValue extracts the value from a .env right-hand side. A quoted
// value is taken verbatim between the quotes (so a '#' inside a connection
// string is preserved). An unquoted value has any trailing inline "# comment"
// stripped, matching common dotenv behavior.
func parseDotEnvValue(val string) string {
	if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') {
		quote := val[0]
		if i := strings.IndexByte(val[1:], quote); i >= 0 {
			return val[1 : 1+i]
		}
		return strings.Trim(val, `"'`)
	}
	if i := strings.IndexByte(val, '#'); i >= 0 {
		val = val[:i]
	}
	return strings.TrimSpace(val)
}
