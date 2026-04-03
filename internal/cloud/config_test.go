package cloud

import "testing"

func TestConfigFromEnvPrefersCanonicalNames(t *testing.T) {
	t.Setenv("ENGRAM_DATABASE_URL", "postgres://canonical@localhost/canonical")
	t.Setenv("ENGRAM_JWT_SECRET", "canonical-secret-value-with-32-bytes!!")
	t.Setenv("ENGRAM_CLOUD_DSN", "postgres://legacy@localhost/legacy")
	t.Setenv("ENGRAM_CLOUD_JWT_SECRET", "legacy-secret-value-with-32-bytes!!!!")
	t.Setenv("ENGRAM_CLOUD_CORS_ORIGINS", " https://one.example , https://two.example ")
	t.Setenv("ENGRAM_CLOUD_MAX_POOL", "25")

	cfg := ConfigFromEnv()

	if cfg.DSN != "postgres://canonical@localhost/canonical" {
		t.Fatalf("dsn = %q", cfg.DSN)
	}
	if cfg.JWTSecret != "canonical-secret-value-with-32-bytes!!" {
		t.Fatalf("jwt secret = %q", cfg.JWTSecret)
	}
	if len(cfg.CORSOrigins) != 2 || cfg.CORSOrigins[0] != "https://one.example" || cfg.CORSOrigins[1] != "https://two.example" {
		t.Fatalf("cors origins = %#v", cfg.CORSOrigins)
	}
	if cfg.MaxPool != 25 {
		t.Fatalf("max pool = %d", cfg.MaxPool)
	}
	if cfg.Port != 8080 {
		t.Fatalf("port = %d", cfg.Port)
	}
}

func TestConfigFromEnvParsesAdminEmail(t *testing.T) {
	t.Setenv("ENGRAM_CLOUD_ADMIN", "  admin@example.com  ")

	cfg := ConfigFromEnv()

	if cfg.AdminEmail != "admin@example.com" {
		t.Fatalf("admin email = %q, expected %q", cfg.AdminEmail, "admin@example.com")
	}
}

func TestConfigFromEnvAdminEmailEmpty(t *testing.T) {
	// Ensure ENGRAM_CLOUD_ADMIN is not set.
	t.Setenv("ENGRAM_CLOUD_ADMIN", "")

	cfg := ConfigFromEnv()

	if cfg.AdminEmail != "" {
		t.Fatalf("admin email should be empty, got %q", cfg.AdminEmail)
	}
}

func TestConfigFromEnvFallsBackToLegacyNames(t *testing.T) {
	t.Setenv("ENGRAM_DATABASE_URL", "")
	t.Setenv("ENGRAM_JWT_SECRET", "")
	t.Setenv("ENGRAM_CLOUD_DSN", "postgres://legacy@localhost/legacy")
	t.Setenv("ENGRAM_CLOUD_JWT_SECRET", "legacy-secret-value-with-32-bytes!!!!")
	t.Setenv("ENGRAM_PORT", "9090")

	cfg := ConfigFromEnv()

	if cfg.DSN != "postgres://legacy@localhost/legacy" {
		t.Fatalf("dsn = %q", cfg.DSN)
	}
	if cfg.JWTSecret != "legacy-secret-value-with-32-bytes!!!!" {
		t.Fatalf("jwt secret = %q", cfg.JWTSecret)
	}
	if cfg.Port != 9090 {
		t.Fatalf("port = %d", cfg.Port)
	}
}
