// Package config loads runtime configuration from the environment (and an
// optional .env file in dev). See docs/api.md "Configuration (env)".
package config

import (
	"bufio"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
)

// Config holds all runtime configuration, loaded from the environment.
type Config struct {
	TAURL       string
	TAToken     string
	DatabaseURL string
	Port        string

	// OIDC auth. When AuthDisabled is true (dev), the API skips token checks.
	// Otherwise OIDCIssuer + OIDCClientID are required to validate Bearer JWTs.
	OIDCIssuer   string
	OIDCClientID string
	AuthDisabled bool

	// AdminEmails are the email addresses (from the JWT) that see /healthz
	// details. Comma-separated.
	AdminEmails []string

	// MediaTokenSecret signs the archive_media cookie.
	MediaTokenSecret string
	// PublicURL is the browser-facing origin: the cookie's Secure flag follows
	// its scheme and it is the CORS allowed origin.
	PublicURL string
	// CORSOrigins are extra allowed origins (e.g. the Vite dev server).
	CORSOrigins []string

	AppName  string
	LogLevel slog.Level
}

// Load reads configuration from the environment. If a .env file exists in the
// working directory (or its parent), it is loaded first without overriding
// variables already present in the environment.
func Load() (*Config, error) {
	loadDotEnv(".env")
	loadDotEnv("../.env")

	cfg := &Config{
		TAURL:            strings.TrimRight(os.Getenv("TA_URL"), "/"),
		TAToken:          os.Getenv("TA_TOKEN"),
		DatabaseURL:      os.Getenv("DATABASE_URL"),
		Port:             getenvDefault("PORT", "8080"),
		OIDCIssuer:       os.Getenv("OIDC_ISSUER"),
		OIDCClientID:     os.Getenv("OIDC_CLIENT_ID"),
		AuthDisabled:     os.Getenv("AUTH_DISABLED") == "true",
		AdminEmails:      splitCSV(os.Getenv("ADMIN_EMAILS")),
		MediaTokenSecret: os.Getenv("MEDIA_TOKEN_SECRET"),
		PublicURL:        strings.TrimRight(os.Getenv("PUBLIC_URL"), "/"),
		CORSOrigins:      splitCSV(os.Getenv("CORS_ORIGINS")),
		AppName:          getenvDefault("APP_NAME", "Archive"),
		LogLevel:         parseLevel(os.Getenv("LOG_LEVEL")),
	}

	var missing []string
	if cfg.TAURL == "" {
		missing = append(missing, "TA_URL")
	}
	if cfg.TAToken == "" {
		missing = append(missing, "TA_TOKEN")
	}
	if cfg.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if cfg.MediaTokenSecret == "" {
		missing = append(missing, "MEDIA_TOKEN_SECRET")
	}
	if cfg.PublicURL == "" {
		missing = append(missing, "PUBLIC_URL")
	}
	if !cfg.AuthDisabled {
		if cfg.OIDCIssuer == "" {
			missing = append(missing, "OIDC_ISSUER (or set AUTH_DISABLED=true)")
		}
		if cfg.OIDCClientID == "" {
			missing = append(missing, "OIDC_CLIENT_ID (or set AUTH_DISABLED=true)")
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	if _, err := url.Parse(cfg.TAURL); err != nil {
		return nil, fmt.Errorf("invalid TA_URL: %w", err)
	}
	if _, err := url.Parse(cfg.PublicURL); err != nil {
		return nil, fmt.Errorf("invalid PUBLIC_URL: %w", err)
	}
	return cfg, nil
}

// SecureCookies reports whether the media cookie should carry the Secure
// flag — true for an https PUBLIC_URL, false for plain-http local dev
// (browsers drop Secure cookies over http).
func (c *Config) SecureCookies() bool {
	return strings.HasPrefix(c.PublicURL, "https://")
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseLevel(s string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(strings.TrimSpace(s))); err != nil {
		return slog.LevelInfo
	}
	return l
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// loadDotEnv parses a simple KEY=value .env file. Lines starting with # are
// ignored. Existing environment variables are not overridden.
func loadDotEnv(path string) {
	f, err := os.Open(path) //nolint:gosec // fixed .env path, not user input
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
}
