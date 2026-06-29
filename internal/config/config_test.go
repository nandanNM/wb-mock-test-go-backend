package config

import (
	"testing"
	"time"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when DATABASE_URL is unset, got nil")
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost/db?sslmode=disable")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port: got %q, want 8080", cfg.Port)
	}
	if cfg.RequestTimeout != 30*time.Second {
		t.Errorf("RequestTimeout: got %v, want 30s", cfg.RequestTimeout)
	}
	if cfg.Addr() != "0.0.0.0:8080" {
		t.Errorf("Addr: got %q", cfg.Addr())
	}
}

func TestGetDurationAcceptsBareSeconds(t *testing.T) {
	t.Setenv("READ_TIMEOUT", "5")
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost/db")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ReadTimeout != 5*time.Second {
		t.Errorf("ReadTimeout: got %v, want 5s", cfg.ReadTimeout)
	}
}

func TestParseDotEnvValue(t *testing.T) {
	cases := map[string]string{
		"development          # inline comment": "development",
		"info":                                  "info",
		"*":                                     "*",
		`'postgres://u:p@h/db?a=1#frag'`:        "postgres://u:p@h/db?a=1#frag", // # inside quotes preserved
		`"quoted value"`:                        "quoted value",
		"  spaced  # c":                         "spaced",
	}
	for in, want := range cases {
		if got := parseDotEnvValue(in); got != want {
			t.Errorf("parseDotEnvValue(%q): got %q, want %q", in, got, want)
		}
	}
}

func TestGetCSV(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://a.com, https://b.com ,")
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost/db")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.CORSAllowedOrigins) != 2 {
		t.Fatalf("origins: got %v, want 2 entries", cfg.CORSAllowedOrigins)
	}
}
