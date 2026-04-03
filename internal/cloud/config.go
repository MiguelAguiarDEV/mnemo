// Package cloud provides shared configuration for the Engram Cloud subsystem.
package cloud

import (
	"os"
	"strconv"
	"strings"
)

// ─── Config ──────────────────────────────────────────────────────────────────

// Config holds configuration for the Engram Cloud server and its subsystems.
type Config struct {
	DSN         string   // Postgres connection string
	JWTSecret   string   // Secret for HMAC-SHA256 JWT signing (>= 32 bytes)
	CORSOrigins []string // Allowed CORS origins
	MaxPool     int      // Maximum database connection pool size
	Port        int      // HTTP port for cloud server mode
	AdminEmail  string   // Email of the admin user (enables Admin tab in dashboard)
}

// DefaultConfig returns a Config with sensible defaults for local development.
func DefaultConfig() Config {
	return Config{
		DSN:         "postgres://engram:engram_dev@localhost:5433/engram_cloud?sslmode=disable",
		JWTSecret:   "",
		CORSOrigins: []string{"*"},
		MaxPool:     10,
		Port:        8080,
	}
}

// ConfigFromEnv reads cloud configuration from environment variables,
// falling back to DefaultConfig values when a variable is not set.
//
// Environment variables:
//   - ENGRAM_DATABASE_URL: Postgres connection string (preferred)
//   - ENGRAM_JWT_SECRET: JWT signing secret (preferred, required in production)
//   - ENGRAM_CLOUD_DSN: Postgres connection string (legacy alias)
//   - ENGRAM_CLOUD_JWT_SECRET: JWT signing secret (legacy alias)
//   - ENGRAM_CLOUD_CORS_ORIGINS: Comma-separated list of allowed origins
//   - ENGRAM_CLOUD_MAX_POOL: Maximum DB connection pool size
//   - ENGRAM_PORT: HTTP port for cloud server mode
//   - ENGRAM_CLOUD_ADMIN: Admin user email (enables Admin tab in dashboard)
func ConfigFromEnv() Config {
	cfg := DefaultConfig()

	if v := DatabaseURLFromEnv(); v != "" {
		cfg.DSN = v
	}
	if v := JWTSecretFromEnv(); v != "" {
		cfg.JWTSecret = v
	}
	if v := os.Getenv("ENGRAM_CLOUD_CORS_ORIGINS"); v != "" {
		origins := strings.Split(v, ",")
		trimmed := make([]string, 0, len(origins))
		for _, o := range origins {
			if s := strings.TrimSpace(o); s != "" {
				trimmed = append(trimmed, s)
			}
		}
		cfg.CORSOrigins = trimmed
	}
	if v := os.Getenv("ENGRAM_CLOUD_MAX_POOL"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.MaxPool = n
		}
	}
	if v := os.Getenv("ENGRAM_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Port = n
		}
	}
	if v := os.Getenv("ENGRAM_CLOUD_ADMIN"); v != "" {
		cfg.AdminEmail = strings.TrimSpace(v)
	}

	return cfg
}

// DatabaseURLFromEnv returns the configured Postgres DSN using the canonical
// env name first, then the legacy alias.
func DatabaseURLFromEnv() string {
	return firstEnv("ENGRAM_DATABASE_URL", "ENGRAM_CLOUD_DSN")
}

// JWTSecretFromEnv returns the configured JWT secret using the canonical env
// name first, then the legacy alias.
func JWTSecretFromEnv() string {
	return firstEnv("ENGRAM_JWT_SECRET", "ENGRAM_CLOUD_JWT_SECRET")
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}
